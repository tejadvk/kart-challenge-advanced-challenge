# Food Ordering API — Implementation Documentation

## Contents

1. [Part 1: Architecture & Implementation](#part-1-architecture--implementation) — Components, request flows, implemented features
2. [Part 2: Scaling](#part-2-scaling-to-10000-requestssecond) — Bottlenecks, Go patterns
3. [Part 3: Consistency Model](#part-3-consistency-model) — Products, inventory, coupons
4. [Part 4: Design Choices & Justification](#part-4-design-choices--justification) — Alternatives, pros/cons, rationale
5. [Appendix A: HTTP Status Codes](#appendix-a-http-status-codes--error-format)
6. [Appendix B: File Reference](#appendix-b-file-reference)
7. [Appendix C: Environment Variables](#appendix-c-environment-variables)
8. [Appendix D: Running](#appendix-d-running-the-api)
9. [Appendix E: API Examples](#appendix-e-api-examples)

---

## Executive Summary

This document details the implementation of the Food Ordering API backend written in Go. It covers the complete architecture: products in PostgreSQL with Redis distributed cache and delta updates, inventory management with overbooking prevention, order placement with coupon limits and idempotency, and the admin API for product management.

---

# Part 1: Architecture & Implementation

## 1.1 Implemented Capabilities Summary

| Capability | Implementation |
|------------|----------------|
| **Products** | PostgreSQL + Redis cache; delta updates via Admin API; DB fallback on Redis failure |
| **Inventory** | `inventory` table; optimistic locking (atomic `UPDATE ... WHERE quantity >= $1`); retry on conflict |
| **Orders** | `orders` + `order_items` in PostgreSQL; single transaction with inventory and coupon |
| **Overbooking prevention** | Atomic inventory reserve; 409 on insufficient stock; up to 4 retries with exponential backoff |
| **Coupon validation** | In-memory index; background loader; order path never touches files; case-insensitive |
| **Coupon usage limits** | `coupon_usage` table; config via COUPON_MAX_USES, COUPON_LIMITS_JSON, COUPON_LIMITS_FILE |
| **Idempotency** | `idempotency_keys` table; Idempotency-Key header; TTL cleanup; stuck-key reset |
| **Admin API** | POST/PUT/PATCH/DELETE /admin/product*; GET/PUT /admin/inventory; GET/PUT /admin/coupons |
| **Outbox** | Transactional outbox; Redis Streams or Kafka; worker with retry |
| **Auth** | api_key (POST /order), admin_api_key (/admin/*); optional METRICS_AUTH_TOKEN |
| **Observability** | Prometheus /metrics; slog; path normalization; rate limit exemptions for /health/*, /metrics |
| **Operational** | Graceful shutdown (SIGTERM/SIGINT); health probes; connection pooling; optional read replica |

## 1.2 Architecture Overview

```
                         ┌─────────────────┐
                         │   HTTP Request  │
                         └────────┬────────┘
                                  │
                         ┌────────▼────────┐
                         │  CORS Middleware │ (CORS_ORIGINS)
                         └────────┬────────┘
                                  │
                         ┌────────▼────────┐
                         │ Rate Limit      │ (RATE_LIMIT_RPS; 429 if exceeded)
                         └────────┬────────┘
                                  │
                         ┌────────▼────────────────────────────────┐
                         │           Gorilla Mux Router             │
                         │  GET /product    GET /product/{id}      │
                         │  POST /order     (RequireAPIKey)        │
                         │  POST/PUT/PATCH/DELETE /admin/product* │
                         │                    (RequireAdminAPIKey) │
                         └────────┬────────────────────────────────┘
                                  │
    ┌─────────────────────────────┼─────────────────────────────┐
    │                             │                             │
┌───▼───┐                   ┌────▼────┐                  ┌─────▼─────┐
│Product│                   │  Order  │                  │   Admin   │
│Handler│                   │ Handler │                  │  Handler  │
└───┬───┘                   └────┬────┘                  └─────┬─────┘
    │                            │                             │
    │                      ┌─────▼─────┐                       │
    │                      │  Order    │                       │
    │                      │  Service  │                       │
    │                      └─────┬─────┘                       │
    │                            │                             │
┌───▼────────────────────────────▼─────────────────────────────▼───┐
│                     ProductStore (ProductCache)                   │
│  GetByID(ctx, id)  GetAll(ctx)  — Redis first, DB on miss        │
│  Set/Delete — Admin only; write-through to DB + delta to Redis   │
└───┬──────────────────────────────────────────────────┬──────────┘
    │                                                    │
┌───▼────────────┐  ┌──────────────┐  ┌────────────────▼──────────┐
│     Redis      │  │  PostgreSQL  │  │  ProductDBRepo (fallback)   │
│ product:{id}   │  │  products    │  │  inventory, orders,        │
│ product:ids    │  │  inventory   │  │  order_items, coupon_usage,│
│ (delta cache)  │  │  orders      │  │  idempotency_keys          │
└────────────────┘  └──────────────┘  └───────────────────────────┘
```

---

## 1.3 Component-by-Component Implementation Details

### 1.3.1 Models (`internal/models/models.go`)

**Purpose:** Defines data structures that map to the OpenAPI schema.

| Struct | Fields | JSON Mapping | Use Case |
|--------|--------|--------------|----------|
| `Product` | ID, Name, Price, Category, Image | camelCase | API response for products |
| `ProductImage` | Thumbnail, Mobile, Tablet, Desktop | nested in Product | Responsive image URLs |
| `OrderItemReq` | ProductID, Quantity | snake_case (`productId`, `quantity`) | Request body per item |
| `OrderReq` | Items, CouponCode | `items` required, `couponCode` optional | POST /order body |
| `OrderItem` | ProductID, Quantity | Same as OrderItemReq | Order response items |
| `Order` | ID, Total, Discounts, Items, Products | Full order response | POST /order response |

**Logic:** Struct tags (`json:"fieldName"`) ensure correct serialization. `omitempty` on `CouponCode` means empty strings are omitted from JSON.

---

### 1.3.2 Product Store: Redis Cache + PostgreSQL (`internal/repository/product_cache.go`, `product_db.go`)

**Purpose:** Serve products from Redis (distributed cache) with PostgreSQL as source of truth. Delta updates—no full cache refresh.

**ProductStore interface:**
- `GetByID(ctx, id) (*Product, error)` — used by product handler and order service
- `GetAll(ctx) ([]Product, error)`

**ProductCache (implements ProductStore) — Redis write-through with delta:**

1. **GetByID(ctx, id)**
   - `GET product:{id}` from Redis
   - Hit: unmarshal JSON, return
   - Redis failure (connection error): fall back to DB, best-effort cache repopulation — service stays available
   - Miss (redis.Nil): `ProductDBRepo.GetByID` from PostgreSQL → `SET product:{id}` + `SADD product:ids {id}` → return

2. **GetAll(ctx)**
   - `SMEMBERS product:ids` → list of IDs
   - On Redis failure (SMEMBERS or MGET): fall back to `refreshFromDB` so service stays available
   - If empty: cold start → load all from DB, populate Redis
   - `MGET product:1 product:2 ...` → unmarshal each
   - On any miss: load from DB, `SET` in Redis

3. **Set(ctx, product)** — Admin API only
   - `ProductDBRepo.Upsert` → PostgreSQL
   - `SET product:{id}` + `SADD product:ids {id}` (delta: only this product)

4. **Delete(ctx, id)** — Admin API only
   - `ProductDBRepo.Delete` → PostgreSQL
   - `DEL product:{id}` + `SREM product:ids {id}` (delta)

**ProductDBRepo** — Raw PostgreSQL CRUD: `GetByID`, `GetAll`, `Create`, `Update`, `Upsert`, `Delete`.

**Legacy:** `internal/repository/product.go` (JSON file) is no longer used; kept for reference.

---

### 1.3.3 Coupon Validator (`pkg/coupon/validator.go`)

**Purpose:** Validate promo codes against rules: 8–10 characters, and must appear in at least 2 of 3 gzipped coupon files. **Decoupled from file I/O:** order path never touches disk.

**Design (Option 2: Background loader with atomic swap):**

1. **Initialization (`NewValidatorFromDataDir`)**
   - Resolves paths to `couponbase1.gz`, `couponbase2.gz`, `couponbase3.gz`
   - No file I/O at construction

2. **LoadContent()**
   - Reads and decompresses all 3 files into memory (`[][]byte`)
   - Uppercases content so validation is case-insensitive regardless of file format
   - Replaces in-memory index under lock (atomic swap)
   - Called at startup and by background worker

3. **IsValid(code string)** — **order path; no file I/O**
   - Trim whitespace; normalize to uppercase (case-insensitive)
   - Length check (8–10 chars)
   - Read-lock, access `content [][]byte` (content uppercased at load time)
   - For each file slice: `bytes.Contains(data, codeBytes)`; count hits
   - Valid if `count >= 2`. Works with any file casing (lower/upper/mixed).

4. **StartBackgroundLoader(ctx, interval)**
   - Goroutine periodically calls `LoadContent()` at `COUPON_RELOAD_INTERVAL` (default 5m)
   - File I/O runs only here; order flow unaffected by coupon file changes

**Decoupling:** Coupon file updates, disk latency, or temporary file unavailability do not impact order placement. New content is picked up on next reload; failed reloads are logged, previous content remains valid.

---

### 1.3.4 Order Service (`internal/services/order.go`)

**Purpose:** Process order request in a single DB transaction: idempotency → product resolution → inventory lock/reserve → coupon validation/usage → persist order.

**PlaceOrder(ctx, req, idempotencyKey) — step-by-step:**

1. **Idempotency (if key provided)**
   - `BEGIN` transaction
   - If `idempotencyRepo.Get(key)` → cached order found → return it
   - If `idempotencyRepo.IsProcessing(key)` → return `ErrIdempotencyConflict` (409)
   - `idempotencyRepo.Reserve(key)` → `INSERT ... ON CONFLICT DO NOTHING`
   - If not inserted (duplicate): re-check Get → return cached or 409

2. **Product resolution**
   - Aggregate quantities by product (same product can appear twice in request)
   - For each item: `productStore.GetByID(ctx, productID)` — from Redis/DB
   - Return `ProductNotFoundError` (422) if not found
   - Return `ErrEmptyOrder` (422) if no valid items

3. **Product existence check (inside TX)**
   - `ProductDBRepo.ExistAllInTx(ctx, tx, productIDs)` — single SQL query using `EXCEPT` to find IDs not in `products`
   - O(1) DB round-trips; returns first missing ID for error reporting
   - Guards against admin delete between resolution and inventory reserve
   - Return `ProductNotFoundError` (422) if any product was deleted

4. **Inventory reserve** (optimistic locking, no row locks)
   - For each product: `inventoryRepo.ReserveOptimistic(ctx, tx, productID, qty)` — single atomic `UPDATE ... WHERE quantity >= $1`
   - If `!reserved` → return `InsufficientStockError` (409) with available quantity
   - Retry loop: up to 4 attempts on InsufficientStock with exponential backoff

5. **Coupon validation and usage**
   - If `req.CouponCode != ""` and `couponVal.IsValid(code)`:
     - `maxUses := couponConfig.GetMaxUses(code)` — from env/file
     - `couponUsageRepo.CheckAndIncrement(ctx, tx, code, maxUses)` — returns `ErrCouponLimitExceeded` (422) if limit reached
     - Apply discount: HAPPYHOURS/HAPPYHRS → 18%, BUYGETONE → lowest item free

6. **Persist order**
   - `orderRepo.Create(ctx, tx, order)`
   - If idempotency key: `idempotencyRepo.Complete(ctx, tx, key, order)`

7. **COMMIT**

---

### 1.3.5 Auth Middleware (`internal/middleware/auth.go`)

**RequireAPIKey** — for POST /order
1. Extract `api_key` from header
2. Empty → 401 Unauthorized
3. Not equal to `APIKey()` (env `API_KEY`, default `"apitest"`) → 403 Forbidden
4. Otherwise → `next.ServeHTTP(w, r)`

**RequireAdminAPIKey** — for /admin/product*
1. Extract `admin_api_key` from header
2. Empty → 401 Unauthorized
3. Not equal to `AdminAPIKey()` (env `ADMIN_API_KEY`, default `"admin"`) → 403 Forbidden
4. Otherwise → `next.ServeHTTP(w, r)`

---

### 1.3.6 Handlers

**Product Handler** (`internal/handlers/product.go`)
- `ListProducts`: `store.GetAll(r.Context())` → encode JSON → 200
- `GetProduct`: Extract `productId`, `store.GetByID(r.Context(), id)` → 404 if nil, else 200 + JSON

**Admin Product Handler** (`internal/handlers/admin.go`) — requires `admin_api_key`
- `CreateProduct` (POST): Decode body → validate → `setProductAtomic` (DB+outbox tx, then Redis) → `inventoryRepo.EnsureExists(id, 0)` → 201
- `UpdateProduct` (PUT): Decode body, set ID from path → `setProductAtomic` → 200
- `PatchProduct` (PATCH): Load existing → merge patch → `setProductAtomic` → 200
- `DeleteProduct` (DELETE): `deleteProductAtomic` (DB+outbox tx, then Redis) → 204

**Admin Inventory Handler** (`internal/handlers/admin_inventory.go`) — requires `admin_api_key`
- `ListInventory` (GET /admin/inventory): Returns all product inventory (productId, quantity)
- `UpdateInventory` (PUT /admin/inventory/{productId}): Sets quantity from body `{"quantity": N}`

**Admin Coupon Handler** (`internal/handlers/admin_coupon.go`) — requires `admin_api_key`
- `ListCoupons` (GET /admin/coupons): Returns coupon usage (couponCode, usedCount) and limits (maxUses) from `coupon_usage` and `coupon_limits`
- `UpdateCouponLimit` (PUT /admin/coupons/{code}/limit): Sets max_uses in `coupon_limits` (body: `{"maxUses": N}`, 0 = unlimited); DB limits override env/file config
- `ResetCouponUsage` (PUT /admin/coupons/{code}/reset): Sets used_count to 0 for a coupon

**Order Handler** (`internal/handlers/order.go`)
- Decode JSON into `OrderReq`
- Validate: `items` non-empty, each has `productId`, `quantity > 0`
- Extract `Idempotency-Key` or `X-Idempotency-Key` (max 255 chars)
- `orderService.PlaceOrder(r.Context(), req, idempotencyKey)`
- Map errors → HTTP status:
  - `ProductNotFoundError` → 422
  - `InsufficientStockError` → 409
  - `ErrCouponLimitExceeded` → 422
  - `ErrIdempotencyConflict` → 409
  - `ErrEmptyOrder` → 422
  - Other → 500

---

### 1.3.7 Database Layer

**`internal/database/database.go`**
- Connects to PostgreSQL via `pgxpool`
- `Migrate()`: Creates all tables (see schema below)
- `SeedInventory(ctx, productIDs, initialQty)`: `INSERT ... ON CONFLICT DO NOTHING` for inventory
- `SeedProductsFromJSON(ctx, jsonPath)`: Loads products from JSON, upserts into `products` table

**PostgreSQL schema:**

| Table | Columns |
|-------|---------|
| `inventory` | product_id (PK), quantity, updated_at |
| `orders` | id (UUID PK), total, discounts, created_at |
| `order_items` | order_id (FK), product_id, quantity — PK (order_id, product_id) |
| `coupon_usage` | coupon_code (PK), used_count, updated_at |
| `coupon_limits` | coupon_code (PK), max_uses, updated_at — admin-set limits override env/file |
| `idempotency_keys` | idempotency_key (PK), status, order_id (FK), response_json, created_at |
| `products` | id (PK), name, price, category, image (JSONB), created_at, updated_at |

**`internal/repository/inventory.go`**
- `ReserveOptimistic(ctx, tx, productID, quantity)`: atomic `UPDATE ... WHERE quantity >= $1` — no row locks
- `EnsureExists(ctx, productID, defaultQty)`: `INSERT ... ON CONFLICT DO NOTHING` — for new products from admin

**`internal/repository/order.go`**
- `Create(ctx, tx, order)`: Inserts into `orders` and `order_items`

---

### 1.3.8 Coupon Limits & Idempotency

**`internal/config/coupon.go`** — Externalized, configurable coupon limits (env/file)
- `COUPON_MAX_USES`: Global default limit (0 = unlimited)
- `COUPON_LIMITS_JSON`: Inline JSON, e.g. `{"HAPPYHOURS":50,"BUYGETONE":100}`
- `COUPON_LIMITS_FILE`: Path to JSON file with per-code limits
- Per-code limits override default; file can add/override JSON

**`internal/repository/coupon_limits.go`** — Admin-set limits (persisted in DB)
- `coupon_limits` table: coupon_code, max_uses (0 = unlimited)
- `GetMaxUses(ctx, code)`: Returns DB limit if set; else caller falls back to config
- `SetLimit(ctx, code, maxUses)`: Upserts limit (used by Admin API)
- Order service checks DB first, then config

**`internal/repository/coupon_usage.go`**
- `coupon_usage(coupon_code, used_count)` table
- `CheckAndIncrement(ctx, tx, code, maxUses)`: Ensures row exists, locks with `SELECT FOR UPDATE`, checks `used_count < maxUses`, increments
- Returns `ErrCouponLimitExceeded` when limit reached

**`internal/repository/idempotency.go`**
- `idempotency_keys(idempotency_key, status, order_id, response_json)` table
- `Reserve(key)`: `INSERT ... ON CONFLICT DO NOTHING` — first request gets `inserted=true`
- `Get(key)`: Returns cached order if status = `completed`
- `Complete(key, order)`: Stores response after successful order

**Idempotency flow:** If `Idempotency-Key` header present → Reserve key → if duplicate, return cached → else process order → Complete with response.

---

### 1.3.9 Products: DB + Redis + Admin API

**Redis data model (delta updates):**
- `product:{id}` — JSON string per product (e.g. `product:1`, `product:2`)
- `product:ids` — SET of all product IDs for listing (SMEMBERS for GetAll)

**Discount codes** (applied when valid per couponbase files):
- `HAPPYHOURS` / `HAPPYHRS` — 18% off subtotal
- `BUYGETONE` — Cheapest item free

**`internal/repository/product_db.go`** — PostgreSQL persistence
- `GetByID`, `GetAll`, `Create`, `Update`, `Upsert`, `Delete`
- `ExistAllInTx(ctx, tx, productIDs)` — single EXCEPT query; verifies all products exist in TX

**`internal/repository/product_cache.go`** — Write-through, delta
- `GetByID(ctx, id)`: GET from Redis → miss → load from DB → SET in Redis
- `GetAll(ctx)`: SMEMBERS product:ids → MGET product:1 product:2 ... → miss → load from DB
- `Set(ctx, product)`: DB Upsert → SET product:{id} + SADD product:ids (delta)
- `Delete(ctx, id)`: DB Delete → DEL product:{id} + SREM product:ids (delta)

**Admin API** (`/admin/product*`): Create, PUT, PATCH, Delete — each triggers delta update in Redis.

---

### 1.3.10 Main Server (`cmd/api/main.go`)

**Startup sequence:**
1. Connect to PostgreSQL and Redis
2. Run `db.Migrate()` — includes `products` table
3. Seed products from `products.json` into DB
4. Seed inventory for products 1–9
5. Create ProductDBRepo, ProductCache; cold-start populate Redis via `GetAll`
6. Wire ProductCache (ProductStore), ProductDBRepo (for ExistAllInTx) for handlers and OrderService
7. Register routes on Gorilla Mux
8. Wrap with CORS (CORS_ORIGINS; default *), RateLimit (RATE_LIMIT_RPS when set; /health/*, /metrics exempt)
9. Graceful shutdown on SIGTERM/SIGINT; start HTTP server with read/write timeouts

---

## 1.4 Request Flow Summary

### GET /product
```
HTTP → CORS → RateLimit → Mux → ProductHandler.ListProducts → ProductCache.GetAll(ctx)
       → Redis SMEMBERS product:ids, MGET product:*
       → Cache miss → ProductDBRepo.GetAll → SET in Redis
       → JSON → 200
```

### GET /product/{id}
```
HTTP → CORS → RateLimit → Mux → ProductHandler.GetProduct → ProductCache.GetByID(ctx, id)
       → Redis GET product:{id}
       → Cache miss → ProductDBRepo.GetByID → SET in Redis
       → 200 or 404
```

### POST /order (full flow)
```
HTTP → CORS → RateLimit → Mux → RequireAPIKey → OrderHandler.PlaceOrder
       → Extract Idempotency-Key header
       → OrderService.PlaceOrder(ctx, req, idempotencyKey)
       → [Retry loop: up to 4 attempts on InsufficientStock]
       → placeOrderOnce:
         → BEGIN TX
         → [Idempotency] Reserve key or return cached
         → [Products] ProductCache.GetByID for each item
         → [Product existence] ProductDBRepo.ExistAllInTx — single EXCEPT query; guards against admin delete
         → [Inventory] ReserveOptimistic (atomic UPDATE, no SELECT FOR UPDATE)
         → [Coupon] CheckAndIncrement (if valid, within limit)
         → OrderRepo.Create + IdempotencyRepo.Complete + Outbox.Insert
         → COMMIT → 200
```

### Admin: PUT /admin/product/{id}
```
HTTP → CORS → RateLimit → Mux → RequireAdminAPIKey → AdminProductHandler.UpdateProduct
       → setProductAtomic: BEGIN TX
       → ProductDBRepo.UpsertExec(tx) + Outbox.Insert(tx)
       → COMMIT → Redis SET product:{id} + SADD product:ids (delta)
       → 200
```

### Admin: GET /admin/inventory, PUT /admin/inventory/{productId}
```
GET  → AdminInventoryHandler.ListInventory → inventoryRepo.GetAll → 200 + JSON
PUT  → AdminInventoryHandler.UpdateInventory → inventoryRepo.SetQuantity(productId, quantity) → 200
```

### Admin: GET /admin/coupons, PUT /admin/coupons/{code}/limit, PUT /admin/coupons/{code}/reset
```
GET  → AdminCouponHandler.ListCoupons → couponUsageRepo.GetAll + couponLimitsRepo.GetAll → 200 + JSON
PUT  /limit  → AdminCouponHandler.UpdateCouponLimit → couponLimitsRepo.SetLimit(code, maxUses) → 200
PUT  /reset  → AdminCouponHandler.ResetCouponUsage → couponUsageRepo.ResetUsage(code) → 200
```

### Outbox Pattern (Event Publishing)

**Purpose:** Reliable event publishing without distributed transactions. Events are written to an `outbox` table in the same transaction as the business write, then a background worker publishes them to Redis Streams or Kafka.

**Flow:**
1. **Write:** Order or product change is committed in a transaction that also inserts a row into `outbox` with `status='pending'`.
2. **Worker:** Polls `outbox` (e.g. every 2s), claims pending rows with `FOR UPDATE SKIP LOCKED`, publishes via `Publisher`, marks `processed` or `failed`.
3. **Broker:** Configurable via `OUTBOX_BROKER`:
   - **redis** (default): Publishes to Redis Streams (`OUTBOX_STREAM`, default `outbox`).
   - **kafka**: Publishes to Kafka topics (event-type mapped, e.g. `orders.placed`, `products.updated`).

**Events:** `OrderPlaced`, `ProductCreated`, `ProductUpdated`, `ProductDeleted`.

**Files:** `internal/outbox/` — `event.go`, `repository.go`, `publisher.go`, `redis_publisher.go`, `kafka_publisher.go`, `worker.go`.

**Overbooking prevention (implemented):** Inventory uses optimistic locking: single atomic `UPDATE ... WHERE quantity >= $1` — no row locks. If reserve fails → `InsufficientStockError` (409). Retry up to 4 times with exponential backoff + jitter on contention. See [4.1 Inventory Locking](#41-inventory-locking) for design rationale.

---

# Part 2: Scaling to 10,000 Requests/Second

## 2.1 Bottlenecks in Current Design

| Component | Current state | At 10k req/s |
|-----------|----------------|--------------|
| Product store | Redis cache, DB fallback | Redis O(1) per key; fine |
| Coupon validator | In-memory index; background reload; no file I/O on order path | O(n) bytes.Contains over 3 slices; low cost |
| Order service | Optimistic inventory; retry on conflict | Connection pool; much less contention than `SELECT FOR UPDATE` |
| Single process | Stateless; Go goroutines | Horizontal scale behind LB |

## 2.2 Go Patterns for High Throughput

### 2.2.1 Horizontal Scaling
- Run multiple instances behind a load balancer
- Stateless design (no in-process session) allows this
- Use `GOMAXPROCS` to match CPU cores

### 2.2.2 Connection Handling
- Go's `net/http` uses one goroutine per connection
- 10k req/s ≈ 10k concurrent goroutines, which Go handles well
- Consider `server.ReadTimeout`, `WriteTimeout` to avoid stuck connections

### 2.2.3 Coupon Validator
- **In-memory index:** Files loaded at startup; background loader refreshes at `COUPON_RELOAD_INTERVAL`.
- **IsValid:** Pure in-memory `bytes.Contains` on `[][]byte`; no file I/O on order path.
- **Case-insensitive:** Codes normalized to uppercase before matching (e.g. `happyhours` → `HAPPYHOURS`).
- **Thread safety:** `sync.RWMutex` protects content swap; readers hold `RLock` during validation.

### 2.2.4 Rate Limit Exemptions
- `/health/live`, `/health/ready`, `/metrics` are exempt from rate limiting so load balancers and orchestrators remain operational.

### 2.2.5 Database for Orders and Inventory
- Use PostgreSQL or similar with connection pooling
- Consider read replicas for product/inventory reads
- Use prepared statements and connection pools (e.g. `pgx`)

### 2.2.6 Optimistic Inventory Locking
- **ReserveOptimistic:** Single `UPDATE ... WHERE quantity >= $1` — no blocking.
- **Retry on InsufficientStock:** Up to 4 attempts with exponential backoff (5ms, 10ms, 20ms base) + jitter.
- **Impact:** Same-product throughput no longer serialized; concurrent orders proceed in parallel. Hot products scale with DB capacity rather than lock hold time.

---

# Part 3: Consistency Model

## 3.1 Target Consistency Model

- **Products:** Catalog (id, name, price, category, image) — eventually consistent is OK.
- **Inventory:** Stock levels must be strongly consistent to avoid overbooking.
- **Coupons:** Validation rules and usage limits must be consistent.

## 3.2 Implementation Approaches in Go

### 3.2.1 Option A: Database Transactions (Single DB)

**Idea:** Use transactions to reserve inventory and record the order atomically.

```go
func (s *OrderService) PlaceOrder(ctx context.Context, req OrderReq) (*Order, error) {
    tx, err := s.db.BeginTx(ctx, nil)
    if err != nil { return nil, err }
    defer tx.Rollback()

    // 1. Lock and check inventory
    for _, item := range req.Items {
        var avail int
        err := tx.QueryRow(
            `SELECT quantity FROM inventory WHERE product_id = $1 FOR UPDATE`,
            item.ProductID,
        ).Scan(&avail)
        if err != nil || avail < item.Quantity {
            return nil, ErrInsufficientStock
        }
    }

    // 2. Decrement inventory
    for _, item := range req.Items {
        _, err := tx.Exec(
            `UPDATE inventory SET quantity = quantity - $1 WHERE product_id = $2`,
            item.Quantity, item.ProductID,
        )
        if err != nil { return nil, err }
    }

    // 3. Validate and apply coupon (separate coupon_usage table if needed)
    // 4. Insert order and order_items
    // 5. tx.Commit()
}
```

`FOR UPDATE` locks rows so concurrent orders block until the first commits or rolls back.

---

### 3.2.2 Option B: Optimistic Locking

**Idea:** Assume no conflict; check version at commit.

```go
// Schema: inventory(product_id, quantity, version)
_, err := tx.Exec(`
    UPDATE inventory SET quantity = quantity - $1, version = version + 1
    WHERE product_id = $2 AND version = $3 AND quantity >= $1
`, item.Quantity, item.ProductID, expectedVersion)
if err != nil || rowsAffected == 0 {
    return ErrConflict  // Retry or reject
}
```

---

### 3.2.3 Option C: Distributed Locking (Redis)

**Idea:** Lock per product before modifying inventory.

```go
lockKey := "inventory:lock:" + productID
ok, err := redis.SetNX(ctx, lockKey, "1", 5*time.Second).Result()
if !ok { return ErrLockNotAcquired }
defer redis.Del(ctx, lockKey)

// Check and decrement inventory (in DB or Redis)
```

---

### 3.2.4 Coupon Usage Limits (Sync)

```go
// In PlaceOrder, after coupon validation:
if req.CouponCode != "" {
    if !couponVal.IsValid(req.CouponCode) {
        return nil, ErrInvalidCoupon
    }
    used, err := s.incrementCouponUsage(ctx, tx, req.CouponCode)
    if err != nil || used > coupon.MaxUses {
        return nil, ErrCouponExhausted
    }
}
```

---

## 3.3 Recommended Architecture for 10k req/s + Consistency

```
                    ┌──────────────────┐
                    │  Load Balancer   │
                    └────────┬─────────┘
                             │
         ┌───────────────────┼───────────────────┐
         │                   │                   │
    ┌────▼────┐         ┌────▼────┐         ┌────▼────┐
    │  API    │         │  API    │         │  API    │
    │ Node 1  │         │ Node 2  │         │ Node N  │
    └────┬────┘         └────┬────┘         └────┬────┘
         │                   │                   │
         └───────────────────┼───────────────────┘
                             │
                    ┌────────▼────────┐
                    │   PostgreSQL   │
                    │  (Inventory,   │
                    │   Orders)      │
                    └────────┬────────┘
                             │
                    ┌────────▼────────┐
                    │  Redis          │
                    │  Product cache  │
                    │  (product:{id}, │
                    │   product:ids)  │
                    └─────────────────┘
```

1. **Products:** PostgreSQL + Redis; served from Redis; delta updates via Admin API.
2. **Inventory:** PostgreSQL; optimistic locking (atomic UPDATE); retry on conflict.
3. **Coupons:** Validated via in-memory index (loaded from couponbase files at startup + periodic reload); usage in `coupon_usage` table; order path never touches files.
4. **Orders:** Inserted in same transaction as inventory update and coupon usage.

---

# Part 4: Design Choices & Justification

This section documents the available alternatives for key design decisions and justifies the current implementation.

---

## Operational Details (Implemented)

Connection pool, health probes, read replica, metrics, and logging are implemented as follows:
- **Connection pool:** `DB_POOL_MIN_CONNS`, `DB_POOL_MAX_CONNS`; pgxpool for primary and replica
- **Health probes:** `GET /health/live` (process alive); `GET /health/ready` (DB + Redis reachable)
- **Read replica:** `DATABASE_READ_REPLICA_URL`; ProductDBRepo routes GetByID/GetAll to replica; writes to primary
- **Metrics:** Prometheus `/metrics`; path normalization; `METRICS_AUTH_TOKEN` for auth when set
- **Logging:** `LOG_JSON=1` for JSON slog output
- **Graceful shutdown:** SIGTERM/SIGINT triggers `server.Shutdown`, cancels workers
- **Idempotency TTL:** Hourly cleanup of keys older than `IDEMPOTENCY_TTL_HOURS`; `ResetStaleProcessing` for stuck processing keys
- **ProductCache fallback:** On Redis failure, fall back to DB; service stays available

---

## 4.1 Inventory Locking

| Option | Description | Pros | Cons |
|--------|-------------|------|------|
| **Pessimistic (`SELECT FOR UPDATE`)** | Lock row before read/update | Simple; strong consistency | Serializes all orders for same product; ~50–200/sec bottleneck on hot products |
| **Optimistic (implemented)** | Single `UPDATE ... WHERE quantity >= $1` | No blocking; high concurrency; scales with DB capacity | Retry needed on conflict; brief race window |
| **Version/optimistic with column** | Add `version` column, check at commit | Standard pattern | Extra column; more complex SQL |
| **Distributed lock (Redis)** | Lock per product before DB | Fine-grained control | Adds Redis dependency; lock expiry complexity |

**Chosen:** Optimistic locking. Maximizes throughput for hot products while keeping implementation simple. Retry (4 attempts, exponential backoff) handles transient conflicts.

---

## 4.2 Product Cache Strategy

| Option | Description | Pros | Cons |
|--------|-------------|------|------|
| **Redis write-through (implemented)** | DB as source of truth; Redis for reads; delta updates | Fast reads; no full refresh | Redis single point; eventual consistency on admin writes |
| **Cache-aside** | App checks cache, loads DB on miss, populates cache | Standard pattern | Cache stampede risk without locking |
| **Write-through with TTL** | Redis keys expire | Automatic invalidation | Stale reads until expire; cold thundering |
| **No cache** | All reads from PostgreSQL | Simple; strong consistency | DB load; slower at scale |

**Chosen:** Redis write-through with delta updates. Products are catalog data where eventual consistency is acceptable. Delta updates avoid full cache flushes.

---

## 4.3 Event Publishing (Outbox)

| Option | Description | Pros | Cons |
|--------|-------------|------|------|
| **Transactional outbox (implemented)** | Insert into outbox in same tx as business write; worker publishes | At-least-once; no distributed tx | Polling latency; worker dependency |
| **Dual-write** | Write to DB and broker in same request | Lower latency | Risk of inconsistency if broker fails |
| **CDC (Debezium, etc.)** | DB binlog → broker | No app changes; strong consistency | Operationally complex; schema coupling |
| **Fire-and-forget** | Publish after commit | Simple | Events lost on broker failure |

**Chosen:** Transactional outbox. Guarantees events are captured with business data. Worker retries failed events (configurable). Abstract Publisher allows Redis Streams or Kafka.

---

## 4.4 Authentication

| Option | Description | Pros | Cons |
|--------|-------------|------|------|
| **Header API key (implemented)** | `api_key` / `admin_api_key` headers | Simple; stateless | Key in headers; single key per role (configurable via env) |
| **JWT** | Bearer token with claims | Stateless; fine-grained | More complex; token refresh; verification overhead |
| **OAuth2** | Full OAuth flow | Industry standard | Heavier; client complexity |
| **mTLS** | Client certificates | Strong security | Certificate management; ops complexity |

**Chosen:** Header API key. Fits simple service-to-service and internal API use. `API_KEY` and `ADMIN_API_KEY` configurable via env. `/metrics` optionally protected by `METRICS_AUTH_TOKEN`.

---

## 4.5 Idempotency

| Option | Description | Pros | Cons |
|--------|-------------|------|------|
| **DB table (implemented)** | `idempotency_keys` with status, response | Strong consistency; survives restarts | Table growth without cleanup |
| **Redis** | Key → response cache | Fast | Not durable; eviction loses keys |
| **DB with TTL cleanup (implemented)** | Delete keys older than N hours | Bounded growth; configurable | Old keys not reusable; 24h default |

**Chosen:** DB table with TTL cleanup. Hourly job deletes keys older than `IDEMPOTENCY_TTL_HOURS` (default 24). Keeps table size bounded while preserving recent keys.

---

## 4.6 Metrics Path Cardinality

| Option | Description | Pros | Cons |
|--------|-------------|------|------|
| **Raw path (e.g. /product/1)** | Actual request path | Accurate per resource | High cardinality; metric explosion |
| **Normalized (implemented)** | `/product/{id}` for `/product/123` | Low cardinality; stable dashboards | Loses per-ID breakdown |
| **Route template from mux** | Use Gorilla mux route name | Precise per-route | Requires router integration |

**Chosen:** Normalized path. Replaces `/product/{id}` and `/admin/product/{id}` segments to keep cardinality low. Sufficient for request rate and latency by endpoint.

---

## 4.7 Admin Product + Outbox Atomicity

| Option | Description | Pros | Cons |
|--------|-------------|------|------|
| **Best-effort (previous)** | Product write then outbox insert | Simple | Outbox can fail after product; events lost |
| **Transactional (implemented)** | Product + outbox in same tx; Redis after commit | Event guaranteed with product | Slightly more code; Redis after tx |
| **Outbox only** | No product events | No admin event loss | Downstream cannot react to product changes |

**Chosen:** Transactional. `setProductAtomic` and `deleteProductAtomic` run product DB write and outbox insert in one transaction, then sync Redis. Ensures events are never lost due to outbox insert failure.

---

## 4.8 Coupon File Decoupling

| Option | Description | Pros | Cons |
|--------|-------------|------|------|
| **Lazy file read (previous)** | Open files on first validation; cache result | Low memory | File I/O on order path; latency; coupling |
| **In-memory + background reload (implemented)** | Load files at startup; periodic reload; atomic swap | No file I/O on order path; coupon changes don't impact orders | Higher memory; eventual consistency |
| **Preload index (Bloom/set)** | Extract codes, build set at startup | O(1) lookup; compact | Requires parseable format; restart to refresh |
| **DB/Redis for codes** | Store valid codes in DB | No file dependency; admin-friendly | Schema; ETL; another dependency |

**Chosen:** In-memory decompressed content with background loader. Keeps file-based source; `IsValid` does only `bytes.Contains` on in-memory slices. `COUPON_RELOAD_INTERVAL` (default 5m) controls how often files are re-read. Coupon file updates, disk latency, and temporary file unavailability no longer affect order placement.

---

# Appendix A: HTTP Status Codes & Error Format

| Status | When |
|--------|------|
| 200 | Success (GET, PUT, PATCH, POST /order) |
| 201 | Product created (POST /admin/product) |
| 204 | Product deleted (DELETE /admin/product/{id}) |
| 400 | Invalid JSON, Idempotency-Key too long |
| 401 | Missing api_key or admin_api_key |
| 403 | Invalid api_key or admin_api_key |
| 404 | Product not found (GET /product/{id}) |
| 409 | Insufficient stock, IdempotencyConflict |
| 422 | Product not found in order, Empty order, Coupon limit exceeded, Validation (items, quantity) |
| 500 | Internal server error |

Error responses: `{"message": "..."}`

---

# Appendix B: File Reference

| File | Responsibility |
|------|----------------|
| `cmd/api/main.go` | Bootstrap: PostgreSQL, Redis, migrations, seed products/inventory, cold-start Redis cache, wire handlers, graceful shutdown (SIGTERM/SIGINT), start server |
| `internal/database/database.go` | DB connection; Migrate (all tables); SeedInventory; SeedProductsFromJSON |
| `internal/models/models.go` | Request/response structs |
| `internal/repository/product.go` | Product catalog (JSON) — legacy, replaced by ProductDBRepo + ProductCache |
| `internal/repository/product_db.go` | Product persistence; ExistAllInTx (product existence in TX, single query) |
| `internal/repository/product_cache.go` | Redis cache with delta updates, ProductStore interface |
| `internal/redis/redis.go` | Redis client (parses REDIS_URL, defaults to localhost:6379) |
| `internal/handlers/admin.go` | Admin API: create/update/patch/delete products |
| `internal/handlers/admin_inventory.go` | Admin inventory: GET /admin/inventory, PUT /admin/inventory/{productId} |
| `internal/handlers/admin_coupon.go` | Admin coupons: GET /admin/coupons, PUT /admin/coupons/{code}/limit, PUT /admin/coupons/{code}/reset |
| `internal/repository/coupon_limits.go` | Coupon limits table; GetMaxUses, SetLimit, GetAll (DB overrides config) |
| `internal/repository/inventory.go` | Inventory: ReserveOptimistic (atomic UPDATE), Reserve (legacy wrapper) |
| `internal/repository/order.go` | Order persistence (INSERT orders, order_items) |
| `internal/repository/coupon_usage.go` | Coupon usage tracking, limit check |
| `internal/repository/idempotency.go` | Idempotency key lookup and storage |
| `internal/config/coupon.go` | Coupon limits config (env + file) |
| `internal/services/order.go` | Order + discount logic; optimistic inventory; retry on InsufficientStock; coupon limits; idempotency |
| `internal/handlers/product.go` | GET /product, GET /product/{id} |
| `internal/handlers/order.go` | POST /order |
| `internal/outbox/event.go` | Event types, NewEvent |
| `internal/outbox/repository.go` | Insert (in tx), ClaimPending, MarkProcessed/Failed |
| `internal/outbox/publisher.go` | Publisher interface |
| `internal/outbox/redis_publisher.go` | Redis Streams adapter |
| `internal/outbox/kafka_publisher.go` | Kafka adapter |
| `internal/outbox/worker.go` | Poll outbox, publish, mark processed |
| `internal/health/health.go` | Liveness and readiness probes (/health/live, /health/ready) |
| `internal/observability/metrics.go` | Prometheus metrics middleware, /metrics handler |
| `internal/observability/logging.go` | Structured logging (slog, LOG_JSON) |
| `internal/middleware/auth.go` | api_key, admin_api_key enforcement |
| `internal/middleware/ratelimit.go` | Rate limit (RATE_LIMIT_RPS), 429 when exceeded; /health/*, /metrics exempt |
| `pkg/coupon/validator.go` | Promo code validation; in-memory index; background reload |
| `data/products.json` | Product catalog (seed source, migrated to DB) |
| `data/couponbase*.gz` | Valid promo code source files |
| `docker-compose.yml` | PostgreSQL + Redis services for local development |
| `config/coupon_limits.example.json` | Example per-code coupon limits file |

---

# Appendix C: Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DATABASE_URL` | `postgres://postgres:postgres@localhost:5432/kartchallenge?sslmode=disable` | PostgreSQL connection |
| `REDIS_URL` | `redis://localhost:6379/0` | Redis connection |
| `PORT` | `8080` | HTTP server port |
| `INITIAL_INVENTORY` | `100` | Seed quantity per product |
| `ADMIN_API_KEY` | `admin` | API key for /admin/* |
| `COUPON_MAX_USES` | `0` (unlimited) | Default coupon usage limit |
| `COUPON_LIMITS_JSON` | - | Per-code limits, e.g. `{"HAPPYHOURS":50}` |
| `COUPON_LIMITS_FILE` | - | Path to JSON file with per-code limits |
| `COUPON_RELOAD_INTERVAL` | `5m` | How often to reload coupon files into memory (e.g. `5m`, `1h`); file I/O off order path |
| `OUTBOX_BROKER` | `redis` | `redis` or `kafka` |
| `OUTBOX_STREAM` | `outbox` | Redis stream name (when broker=redis) |
| `KAFKA_BROKERS` | `localhost:9092` | Comma-separated Kafka brokers |
| `KAFKA_OUTBOX_TOPIC` | `outbox` | Default Kafka topic for unmapped events |
| `DB_POOL_MIN_CONNS` | (pgx default) | Min connections in DB pool |
| `DB_POOL_MAX_CONNS` | (pgx default) | Max connections in DB pool |
| `DATABASE_READ_REPLICA_URL` | - | Read replica URL; product reads use it when set |
| `LOG_JSON` | `0` | Set to `1` for JSON structured logging (slog) |
| `API_KEY` | `apitest` | API key for POST /order |
| `METRICS_AUTH_TOKEN` | - | When set, /metrics requires `Authorization: Bearer <token>` or `?token=<token>` |
| `IDEMPOTENCY_TTL_HOURS` | `24` | Delete idempotency keys older than this (hourly cleanup) |
| `IDEMPOTENCY_STALE_PROCESSING_MINUTES` | `5` | Reset stuck 'processing' keys older than this (allows retries when request crashed) |
| `RATE_LIMIT_RPS` | - | When set, global rate limit (requests/sec). 429 when exceeded. |
| `CORS_ORIGINS` | `*` | Comma-separated allowed origins; empty/unset = allow all |

---

# Appendix D: Running the API

**Prerequisites:** PostgreSQL, Redis

**Local run (API on port 8080):**
```bash
cd backend-challenge
docker compose up -d postgres redis
go run ./cmd/api
```

**Full stack with Docker** (from project root; backend on port 8081):
```bash
docker compose up -d
# Frontend: http://localhost:3000 | Backend: http://localhost:8081
```

---

# Appendix E: API Examples

Use `localhost:8080` for local run, `localhost:8081` for Docker.

```bash
# List products (from Redis cache)
curl http://localhost:8080/product

# Get product by ID
curl http://localhost:8080/product/1

# Place order (requires api_key)
curl -X POST http://localhost:8080/order \
  -H "Content-Type: application/json" \
  -H "api_key: apitest" \
  -d '{"items":[{"productId":"1","quantity":2}],"couponCode":"HAPPYHOURS"}'

# Place order with idempotency (prevents duplicates on retry)
curl -X POST http://localhost:8080/order \
  -H "Content-Type: application/json" \
  -H "api_key: apitest" \
  -H "Idempotency-Key: $(uuidgen)" \
  -d '{"items":[{"productId":"1","quantity":2}]}'

# Admin: Create product (requires admin_api_key)
curl -X POST http://localhost:8080/admin/product \
  -H "Content-Type: application/json" \
  -H "admin_api_key: admin" \
  -d '{"id":"10","name":"New Dessert","price":5,"category":"Dessert","image":{"thumbnail":"","mobile":"","tablet":"","desktop":""}}'

# Admin: Update product price (delta - only product:10 in Redis)
curl -X PATCH http://localhost:8080/admin/product/10 \
  -H "Content-Type: application/json" \
  -H "admin_api_key: admin" \
  -d '{"price":6.99}'

# Admin: Delete product
curl -X DELETE http://localhost:8080/admin/product/10 \
  -H "admin_api_key: admin"

# Admin: List inventory
curl http://localhost:8080/admin/inventory -H "admin_api_key: admin"

# Admin: Set inventory quantity
curl -X PUT http://localhost:8080/admin/inventory/1 \
  -H "Content-Type: application/json" \
  -H "admin_api_key: admin" \
  -d '{"quantity":150}'

# Admin: List coupons (usage & limits)
curl http://localhost:8080/admin/coupons -H "admin_api_key: admin"

# Admin: Set coupon limit (0 = unlimited)
curl -X PUT http://localhost:8080/admin/coupons/HAPPYHOURS/limit \
  -H "Content-Type: application/json" \
  -H "admin_api_key: admin" \
  -d '{"maxUses":50}'

# Admin: Reset coupon usage
curl -X PUT http://localhost:8080/admin/coupons/HAPPYHOURS/reset -H "admin_api_key: admin"
```
