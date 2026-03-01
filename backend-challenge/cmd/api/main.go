package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"github.com/rs/cors"
	"github.com/yourusername/kart-challenge/backend-challenge/internal/config"
	"github.com/yourusername/kart-challenge/backend-challenge/internal/database"
	"github.com/yourusername/kart-challenge/backend-challenge/internal/handlers"
	"github.com/yourusername/kart-challenge/backend-challenge/internal/health"
	"github.com/yourusername/kart-challenge/backend-challenge/internal/middleware"
	"github.com/yourusername/kart-challenge/backend-challenge/internal/observability"
	"github.com/yourusername/kart-challenge/backend-challenge/internal/outbox"
	"github.com/yourusername/kart-challenge/backend-challenge/internal/redis"
	"github.com/yourusername/kart-challenge/backend-challenge/internal/repository"
	"github.com/yourusername/kart-challenge/backend-challenge/internal/services"
	"github.com/yourusername/kart-challenge/backend-challenge/pkg/coupon"
)

// parseCORSOrigins returns allowed origins from CORS_ORIGINS env (comma-separated).
// If empty/unset, returns ["*"] for permissive dev mode.
func parseCORSOrigins(env string) []string {
	s := strings.TrimSpace(env)
	if s == "" {
		return []string{"*"}
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if o := strings.TrimSpace(p); o != "" {
			out = append(out, o)
		}
	}
	if len(out) == 0 {
		return []string{"*"}
	}
	return out
}

func runIdempotencyCleanup(ctx context.Context, repo *repository.IdempotencyRepo, ttlHours int) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := repo.DeleteOlderThan(ctx, ttlHours); err != nil {
				log.Printf("[idempotency] cleanup failed: %v", err)
			} else if n > 0 {
				log.Printf("[idempotency] cleaned %d expired key(s)", n)
			}
		}
	}
}

func runIdempotencyStaleReset(ctx context.Context, repo *repository.IdempotencyRepo, staleMinutes int) {
	if staleMinutes <= 0 {
		return
	}
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := repo.ResetStaleProcessing(ctx, staleMinutes); err != nil {
				log.Printf("[idempotency] stale reset failed: %v", err)
			} else if n > 0 {
				log.Printf("[idempotency] reset %d stale processing key(s)", n)
			}
		}
	}
}

func main() {
	observability.InitLogger()
	ctx := context.Background()

	// Resolve data paths relative to working directory
	dataDir := "data"
	if wd, err := os.Getwd(); err == nil {
		if filepath.Base(wd) == "api" {
			dataDir = filepath.Join("..", "data")
		} else if filepath.Base(wd) == "backend-challenge" {
			dataDir = "data"
		}
	}

	// Database connection with configurable pool
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:postgres@localhost:5432/kartchallenge?sslmode=disable"
		log.Printf("Using default DATABASE_URL (set DATABASE_URL to override)")
	}
	dbCfg := database.ConfigFromEnv(dbURL)
	dbCfg.URL = dbURL

	db, err := database.New(ctx, dbCfg)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Redis connection
	rdb, err := redis.New(ctx, redis.Config{})
	if err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}
	defer rdb.Close()

	// Migrate schema
	if err := db.Migrate(ctx); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	// Seed products from JSON (if products table empty or for initial load)
	productsPath := filepath.Join(dataDir, "products.json")
	if err := db.SeedProductsFromJSON(ctx, productsPath); err != nil {
		log.Printf("Warning: could not seed products: %v", err)
	}

	// Seed inventory for products 1-9
	productIDs := []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}
	initialQty := 100
	if qty := os.Getenv("INITIAL_INVENTORY"); qty != "" {
		if n, err := strconv.Atoi(qty); err == nil && n >= 0 {
			initialQty = n
		}
	}
	if err := db.SeedInventory(ctx, productIDs, initialQty); err != nil {
		log.Fatalf("Failed to seed inventory: %v", err)
	}

	// Product DB repo and Redis cache (delta updates). Optional read replica for product reads.
	var productCache *repository.ProductCache
	var productDB *repository.ProductDBRepo
	var replicaPool interface{ Close() }
	if replicaURL := os.Getenv("DATABASE_READ_REPLICA_URL"); replicaURL != "" {
		pool, err := database.NewReadReplica(ctx, replicaURL, dbCfg)
		if err != nil {
			log.Fatalf("Failed to connect to read replica: %v", err)
		}
		if pool != nil {
			replicaPool = pool
			productDB = repository.NewProductDBRepoWithReplica(db.Pool, pool)
			productCache = repository.NewProductCache(rdb, productDB)
			log.Printf("Read replica enabled for product reads")
		}
	}
	if productCache == nil {
		productDB = repository.NewProductDBRepo(db.Pool)
		productCache = repository.NewProductCache(rdb, productDB)
	}
	if replicaPool != nil {
		defer replicaPool.Close()
	}

	// Cold start: populate Redis from DB if empty
	_, _ = productCache.GetAll(ctx)

	inventoryRepo := repository.NewInventoryRepo(db.Pool)
	orderRepo := repository.NewOrderRepo(db.Pool)
	couponUsageRepo := repository.NewCouponUsageRepo(db.Pool)
	couponLimitsRepo := repository.NewCouponLimitsRepo(db.Pool)
	idempotencyRepo := repository.NewIdempotencyRepo(db.Pool)
	couponVal := coupon.NewValidatorFromDataDir(dataDir)
	if err := couponVal.CheckFilesExist(); err != nil {
		log.Printf("Warning: coupon file missing or unreadable: %v (validation may be degraded)", err)
	}
	// Load coupon files into memory (decouples order path from file I/O)
	var cancelCoupon context.CancelFunc = func() {}
	if err := couponVal.LoadContent(); err != nil {
		log.Printf("Warning: could not load coupon files: %v (validation disabled until reload)", err)
	} else {
		couponReload := 5 * time.Minute
		if v := os.Getenv("COUPON_RELOAD_INTERVAL"); v != "" {
			if d, err := time.ParseDuration(v); err == nil && d > 0 {
				couponReload = d
			}
		}
		couponCtx, cancel := context.WithCancel(ctx)
		cancelCoupon = cancel
		couponVal.StartBackgroundLoader(couponCtx, couponReload)
		log.Printf("Coupon: loaded, background reload every %s", couponReload)
	}
	defer cancelCoupon()
	couponConfig := config.NewCouponLimitsConfig()

	// Outbox: repository and publisher (Redis or Kafka)
	outboxRepo := outbox.NewRepository(db.Pool)
	var publisher outbox.Publisher
	broker := strings.ToLower(strings.TrimSpace(os.Getenv("OUTBOX_BROKER")))
	if broker == "" {
		broker = "redis"
	}
	switch broker {
	case "redis":
		stream := os.Getenv("OUTBOX_STREAM")
		if stream == "" {
			stream = "outbox"
		}
		publisher = outbox.NewRedisPublisher(outbox.RedisPublisherConfig{
			Client: rdb,
			Stream: stream,
		})
		log.Printf("Outbox: Redis publisher, stream=%s", stream)
	case "kafka":
		brokerStr := strings.TrimSpace(os.Getenv("KAFKA_BROKERS"))
		brokers := strings.Split(brokerStr, ",")
		for i := range brokers {
			brokers[i] = strings.TrimSpace(brokers[i])
		}
		if brokerStr == "" || (len(brokers) == 1 && brokers[0] == "") {
			brokers = []string{"localhost:9092"}
		}
		publisher = outbox.NewKafkaPublisher(outbox.KafkaPublisherConfig{
			Brokers:      brokers,
			DefaultTopic: os.Getenv("KAFKA_OUTBOX_TOPIC"),
		})
		log.Printf("Outbox: Kafka publisher, brokers=%v", brokers)
	default:
		log.Printf("Outbox: unknown broker %q, using Redis", broker)
		publisher = outbox.NewRedisPublisher(outbox.RedisPublisherConfig{Client: rdb})
	}
	defer publisher.Close()

	// Outbox worker
	workerCfg := outbox.WorkerConfig{
		PollInterval:       2 * time.Second,
		BatchSize:          10,
		StaleResetMinutes:  5,
		FailedRetryMinutes: 5,
		FailedRetryBatch:  10,
	}
	worker := outbox.NewWorker(outboxRepo, publisher, workerCfg)
	workerCtx, cancelWorker := context.WithCancel(ctx)
	defer cancelWorker()
	go worker.Run(workerCtx)

	// Idempotency key cleanup (TTL: delete keys older than IDEMPOTENCY_TTL_HOURS, default 24)
	idempotencyTTL := 24
	if v := os.Getenv("IDEMPOTENCY_TTL_HOURS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			idempotencyTTL = n
		}
	}
	idempotencyCleanupCtx, cancelIdempotency := context.WithCancel(ctx)
	defer cancelIdempotency()
	go runIdempotencyCleanup(idempotencyCleanupCtx, idempotencyRepo, idempotencyTTL)

	// Reset stuck 'processing' keys (request crashed before Complete) — allows retries sooner
	idempotencyStaleMinutes := 5
	if v := os.Getenv("IDEMPOTENCY_STALE_PROCESSING_MINUTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			idempotencyStaleMinutes = n
		}
	}
	idempotencyStaleCtx, cancelIdempotencyStale := context.WithCancel(ctx)
	defer cancelIdempotencyStale()
	go runIdempotencyStaleReset(idempotencyStaleCtx, idempotencyRepo, idempotencyStaleMinutes)

	orderService := services.NewOrderService(db.Pool, productCache, productDB, inventoryRepo, orderRepo,
		couponUsageRepo, couponLimitsRepo, idempotencyRepo, outboxRepo, couponVal, couponConfig)

	productHandler := handlers.NewProductHandler(productCache)
	adminProductHandler := handlers.NewAdminProductHandler(productCache, productDB, inventoryRepo, outboxRepo, db.Pool)
	adminInventoryHandler := handlers.NewAdminInventoryHandler(inventoryRepo)
	adminCouponHandler := handlers.NewAdminCouponHandler(couponUsageRepo, couponLimitsRepo)
	orderHandler := handlers.NewOrderHandler(orderService)

	r := mux.NewRouter()

	// Health checks (for load balancers, Kubernetes, Docker)
	healthHandler := health.NewHandler(db.Pool, rdb)
	r.HandleFunc("/health/live", healthHandler.Live).Methods("GET")
	r.HandleFunc("/health/ready", healthHandler.Ready).Methods("GET")

	// Prometheus metrics
	r.Handle("/metrics", observability.Handler()).Methods("GET")

	// Product routes (no auth required)
	r.HandleFunc("/product", productHandler.ListProducts).Methods("GET")
	r.HandleFunc("/product/{productId}", productHandler.GetProduct).Methods("GET")

	// Admin product routes (requires admin_api_key)
	adminProduct := r.PathPrefix("/admin/product").Subrouter()
	adminProduct.Use(middleware.RequireAdminAPIKey)
	adminProduct.HandleFunc("", adminProductHandler.CreateProduct).Methods("POST")
	adminProduct.HandleFunc("/{productId}", adminProductHandler.UpdateProduct).Methods("PUT")
	adminProduct.HandleFunc("/{productId}", adminProductHandler.PatchProduct).Methods("PATCH")
	adminProduct.HandleFunc("/{productId}", adminProductHandler.DeleteProduct).Methods("DELETE")

	// Admin inventory routes (requires admin_api_key)
	adminInventory := r.PathPrefix("/admin/inventory").Subrouter()
	adminInventory.Use(middleware.RequireAdminAPIKey)
	adminInventory.HandleFunc("", adminInventoryHandler.ListInventory).Methods("GET")
	adminInventory.HandleFunc("/{productId}", adminInventoryHandler.UpdateInventory).Methods("PUT")

	// Admin coupon routes (requires admin_api_key)
	adminCoupon := r.PathPrefix("/admin/coupons").Subrouter()
	adminCoupon.Use(middleware.RequireAdminAPIKey)
	adminCoupon.HandleFunc("", adminCouponHandler.ListCoupons).Methods("GET")
	adminCoupon.HandleFunc("/{code}/limit", adminCouponHandler.UpdateCouponLimit).Methods("PUT")
	adminCoupon.HandleFunc("/{code}/reset", adminCouponHandler.ResetCouponUsage).Methods("PUT")

	// Order route (requires api_key header)
	r.Handle("/order", middleware.RequireAPIKey(http.HandlerFunc(orderHandler.PlaceOrder))).Methods("POST")

	// CORS: CORS_ORIGINS comma-separated; if empty/unset, allow all (*)
	// AllowCredentials=false when using * (CORS spec: credentials not allowed with wildcard origin)
	corsOrigins := parseCORSOrigins(os.Getenv("CORS_ORIGINS"))
	allowCreds := true
	for _, o := range corsOrigins {
		if o == "*" {
			allowCreds = false
			break
		}
	}
	c := cors.New(cors.Options{
		AllowedOrigins:   corsOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"*"},
		AllowCredentials: allowCreds,
	})
	handler := c.Handler(middleware.RateLimit(observability.Middleware(r)))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	addr := ":" + port
	fmt.Printf("Server starting on http://localhost%s\n", addr)
	fmt.Printf("  GET    /health/live       - Liveness probe\n")
	fmt.Printf("  GET    /health/ready      - Readiness (DB + Redis)\n")
	fmt.Printf("  GET    /metrics           - Prometheus metrics\n")
	fmt.Printf("  GET    /product           - List products (from Redis cache)\n")
	fmt.Printf("  GET    /product/{id}      - Get product by ID\n")
	fmt.Printf("  POST   /order             - Place order (API_KEY env, default apitest)\n")
	fmt.Printf("  POST   /admin/product     - Create product (admin_api_key)\n")
	fmt.Printf("  PUT    /admin/product/{id} - Update product\n")
	fmt.Printf("  PATCH  /admin/product/{id} - Partial update\n")
	fmt.Printf("  DELETE /admin/product/{id} - Delete product\n")
	fmt.Printf("  GET    /admin/inventory - List inventory\n")
	fmt.Printf("  PUT    /admin/inventory/{id} - Update inventory\n")
	fmt.Printf("  GET    /admin/coupons - List coupon usage & limits\n")
	fmt.Printf("  PUT    /admin/coupons/{code}/limit - Set coupon limit\n")
	fmt.Printf("  PUT    /admin/coupons/{code}/reset - Reset coupon usage\n")
	fmt.Printf("  Database: connected, Redis: connected, inventory seeded\n")

	server := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	// Graceful shutdown on SIGTERM/SIGINT
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-stop
		log.Printf("Shutting down...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cancelWorker()
		cancelIdempotency()
		cancelIdempotencyStale()
		cancelCoupon()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("Server shutdown error: %v", err)
		}
	}()

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
	log.Printf("Server stopped")
}
