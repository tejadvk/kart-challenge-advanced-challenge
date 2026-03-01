# Food Ordering API — Architecture & Design Decisions

This document explains the architectural approaches taken in this project, the alternatives considered for each major decision, the rationale for choices made, and how the system scales for production workloads (10k requests/second). It is intended as a reviewer’s guide to the architecture, Go patterns used per module, and all configurable hooks.

---

## Contents

1. [High-Level Architecture](#1-high-level-architecture)
2. [Request Pipeline & Middleware](#2-request-pipeline--middleware) — including Route Summary
3. [Design Decisions (Alternatives, Chosen, Rationale)](#3-design-decisions-alternatives-chosen-rationale)
4. [Go Patterns by Module](#4-go-patterns-by-module)
5. [Configuration & Hooks](#5-configuration--hooks)
6. [Scaling to 10,000 Requests/Second](#6-scaling-to-10000-requestssecond)
7. [Operational Hooks](#7-operational-hooks)

---

# 1. High-Level Architecture

```
                    ┌──────────────┐
                    │ HTTP Request │
                    └──────┬───────┘
                           │
    ┌──────────────────────┼──────────────────────┐
    │ CORS → Rate Limit → Metrics → Gorilla Mux    │
    └──────────────────────┬──────────────────────┘
                            │
          ┌─────────────────┼─────────────────────────┐
          │                 │                         │
    ┌─────▼─────┐     ┌─────▼─────┐     ┌─────────────▼─────────────┐
    │  Product  │     │   Order   │     │  Admin (Product, Inv,     │
    │  Handler  │     │  Handler  │     │  Coupon handlers)         │
    └─────┬─────┘     └─────┬─────┘     └─────────────┬─────────────┘
          │                 │                 │
          │           ┌─────▼─────┐           │
          │           │  Order    │           │
          │           │  Service  │           │
          │           └─────┬─────┘           │
          │                 │                 │
          └─────────────────┼─────────────────┘
                            │
    ┌───────────────────────▼───────────────────────┐
    │         ProductStore (ProductCache)           │
    │  Redis-first, DB fallback; delta updates      │
    └───────────────────────┬───────────────────────┘
                            │
    ┌───────────────────────┼───────────────────────┐
    │ Redis (product:{id})  │  PostgreSQL           │
    │ ProductDBRepo        │  inventory, orders,   │
    │ CouponValidator      │  coupon_usage,         │
    │ Outbox worker        │  coupon_limits,        │
    └──────────────────────┤  idempotency_keys,     │
                           │  products, outbox     │
                           └───────────────────────┘
```

**Data flow:** Products from Redis (or DB fallback); inventory and orders in PostgreSQL; coupon validation in memory; coupon limits from `coupon_limits` table (admin-set) or env/file; events via transactional outbox.

---

# 2. Request Pipeline & Middleware

| Layer | Purpose | Hook / Config |
|-------|---------|---------------|
| CORS | Allow cross-origin requests | `CORS_ORIGINS` (comma-separated; `*` = allow all) |
| Rate Limit | Throttle requests | `RATE_LIMIT_RPS`; `/health/*`, `/metrics` exempt |
| Metrics | Prometheus request count, duration | Path normalization; `METRICS_AUTH_TOKEN` optional |
| Auth | API keys | `API_KEY`, `ADMIN_API_KEY` headers |
| Router | Gorilla Mux | Route registration in `main.go` |

**Order of execution:** CORS → Rate Limit → Metrics → Router → Handler.

### 2.1 Route Summary

| Method | Path | Auth | Handler |
|--------|------|------|---------|
| GET | /product | — | ProductHandler.ListProducts |
| GET | /product/{productId} | — | ProductHandler.GetProduct |
| POST | /order | api_key | OrderHandler.PlaceOrder |
| POST | /admin/product | admin_api_key | AdminProductHandler.CreateProduct |
| PUT | /admin/product/{productId} | admin_api_key | AdminProductHandler.UpdateProduct |
| PATCH | /admin/product/{productId} | admin_api_key | AdminProductHandler.PatchProduct |
| DELETE | /admin/product/{productId} | admin_api_key | AdminProductHandler.DeleteProduct |
| GET | /admin/inventory | admin_api_key | AdminInventoryHandler.ListInventory |
| PUT | /admin/inventory/{productId} | admin_api_key | AdminInventoryHandler.UpdateInventory |
| GET | /admin/coupons | admin_api_key | AdminCouponHandler.ListCoupons |
| PUT | /admin/coupons/{code}/limit | admin_api_key | AdminCouponHandler.UpdateCouponLimit |
| PUT | /admin/coupons/{code}/reset | admin_api_key | AdminCouponHandler.ResetCouponUsage |
| GET | /health/live | — | HealthHandler.Live |
| GET | /health/ready | — | HealthHandler.Ready |
| GET | /metrics | optional METRICS_AUTH_TOKEN | Prometheus metrics |

---

# 3. Design Decisions (Alternatives, Chosen, Rationale)

## 3.1 Inventory Locking

| Option | Description | Pros | Cons |
|--------|-------------|------|------|
| **Pessimistic (`SELECT FOR UPDATE`)** | Lock row before read/update | Simple; strong consistency | All orders for same product serialize; ~50–200/sec bottleneck |
| **Optimistic (chosen)** | Single `UPDATE ... WHERE quantity >= $1` | No blocking; high concurrency | Retry on conflict; brief race window |
| **Version column** | Add `version`, check at commit | Common pattern | Extra column; more complex SQL |
| **Distributed lock (Redis)** | Lock per product via Redis | Fine-grained control | Extra dependency; lock expiry handling |

**Chosen:** Optimistic locking.

**Why:** At 10k req/s, pessimistic locking on hot products would cap throughput. Optimistic locking lets concurrent orders proceed; conflicts are retried (4 attempts, exponential backoff). Throughput scales with DB capacity instead of lock hold time.

**Go usage:** `ReserveOptimistic` runs in the caller’s transaction; minimal `Tx` interface (`QueryRow`, `Exec`).

---

## 3.2 Product Cache Strategy

| Option | Description | Pros | Cons |
|--------|-------------|------|------|
| **Redis write-through + delta (chosen)** | DB source of truth; Redis for reads; per-product updates | Fast reads; no full refresh | Redis dependency; eventual consistency |
| **Cache-aside** | App checks cache, loads DB on miss | Standard pattern | Cache stampede risk |
| **Write-through with TTL** | Redis keys expire | Auto invalidation | Stale reads; cold thundering |
| **No cache** | All reads from PostgreSQL | Simple; strong consistency | Higher DB load at scale |

**Chosen:** Redis write-through with delta updates.

**Why:** Product catalog can be eventually consistent. Delta updates avoid full cache refreshes when admins change products. DB is source of truth; Redis improves read latency.

**Go usage:** `ProductStore` interface; `ProductCache` implements it; `GetByID`/`GetAll` fall back to DB on Redis failure.

---

## 3.3 Event Publishing (Outbox)

| Option | Description | Pros | Cons |
|--------|-------------|------|------|
| **Transactional outbox (chosen)** | Insert into outbox in same tx as business write; worker publishes | At-least-once; no distributed tx | Polling latency; worker process |
| **Dual-write** | Write to DB and broker in same request | Lower latency | Risk of inconsistency if broker fails |
| **CDC (Debezium)** | DB binlog → broker | Strong consistency | Operations; schema coupling |
| **Fire-and-forget** | Publish after commit | Simple | Events lost on broker failure |

**Chosen:** Transactional outbox.

**Why:** Events are stored in the same transaction as business data, so they are not lost on broker failure. A worker polls and publishes; failed events can be retried. No distributed transactions.

**Go usage:** Abstract `Publisher` interface; Redis Streams and Kafka adapters; worker with retry.

---

## 3.4 Authentication

| Option | Description | Pros | Cons |
|--------|-------------|------|------|
| **Header API key (chosen)** | `api_key` / `admin_api_key` | Simple; stateless | Single key per role; in headers |
| **JWT** | Bearer token with claims | Stateless; fine-grained | More complex; token refresh |
| **OAuth2** | Full OAuth flow | Standard | Heavy; client complexity |
| **mTLS** | Client certificates | Strong security | Certificate management |

**Chosen:** Header API keys.

**Why:** Suitable for service-to-service and internal APIs. `API_KEY`, `ADMIN_API_KEY`, and optional `METRICS_AUTH_TOKEN` are configurable via env.

---

## 3.5 Idempotency Storage

| Option | Description | Pros | Cons |
|--------|-------------|------|------|
| **DB table + TTL cleanup (chosen)** | `idempotency_keys` with status, response; hourly cleanup | Strong consistency; survives restarts | Table growth without cleanup |
| **Redis** | Key → response cache | Fast | Not durable; eviction risk |
| **DB without cleanup** | Same as chosen but no TTL | Simple | Unbounded growth |

**Chosen:** DB table with TTL cleanup.

**Why:** Idempotency needs durability across restarts. `IDEMPOTENCY_TTL_HOURS` controls cleanup. `IDEMPOTENCY_STALE_PROCESSING_MINUTES` resets stuck “processing” keys when a request crashes before completing.

---

## 3.6 Coupon File Handling

| Option | Description | Pros | Cons |
|--------|-------------|------|------|
| **In-memory + background reload (chosen)** | Load at startup; periodic reload; atomic swap | No file I/O on order path | Higher memory; eventual consistency |
| **Lazy file read** | Open files on first validation; cache result | Low memory | File I/O on order path; latency |
| **Preload index (Bloom/set)** | Extract codes, build set at startup | O(1) lookup | Needs parseable format; restart to refresh |
| **DB/Redis for codes** | Store valid codes in DB | No file dependency | Schema; ETL |

**Chosen:** In-memory decompressed content with background loader.

**Why:** Coupon validation must not block on file I/O. Files are loaded at startup and reloaded periodically (`COUPON_RELOAD_INTERVAL`). `IsValid` does `bytes.Contains` on in-memory slices only. File content is uppercased for case-insensitive matching.

---

## 3.7 Coupon Usage Limits (Admin vs Config)

| Option | Description | Pros | Cons |
|--------|-------------|------|------|
| **Env/file only** | Limits from COUPON_MAX_USES, COUPON_LIMITS_JSON, COUPON_LIMITS_FILE | Simple; no DB schema | Requires deploy/config change to adjust |
| **DB + config fallback (chosen)** | `coupon_limits` table; admin API sets limits; order service checks DB first | Admin can adjust limits without deploy; config as default | Two sources; precedence rules |
| **DB only** | All limits in DB | Single source | No env override; cold start empty |

**Chosen:** `coupon_limits` table with config fallback.

**Why:** Admin UI and API (`PUT /admin/coupons/{code}/limit`) need to persist limits. Order service checks `coupon_limits` first; if no DB row, falls back to env/file config. Env/file remain useful for defaults and non-admin environments.

**Go usage:** `CouponLimitsRepo.GetMaxUses(ctx, code)`; `OrderService.getMaxUses(ctx, code)` checks DB then config.

---

## 3.8 Admin Product + Outbox Atomicity

| Option | Description | Pros | Cons |
|--------|-------------|------|------|
| **Transactional (chosen)** | Product + outbox in same tx; Redis after commit | Event never lost | More code; Redis sync after tx |
| **Best-effort** | Product write then outbox insert | Simple | Outbox can fail; events lost |
| **Outbox only** | No product events | No event loss | Downstream cannot react to changes |

**Chosen:** Transactional product + outbox.

**Why:** Product and outbox rows are written in one transaction; Redis is updated after commit. Ensures events are not lost due to outbox insert failure.

---

## 3.9 Product Existence Check (Order Flow)

| Option | Description | Pros | Cons |
|--------|-------------|------|------|
| **ExistAllInTx with EXCEPT query (chosen)** | Single SQL query to find IDs not in `products` | O(1) round-trips | - |
| **No check** | Trust product resolution | Simple | Orders possible for deleted products |
| **O(n) per-product queries** | Query each product in loop | Clear logic | O(n) round-trips |

**Chosen:** Single `EXCEPT` query inside the order transaction.

**Why:** Guards against admin deleting a product between resolution and inventory reserve. One query finds any missing IDs with O(1) DB round-trips.

---

## 3.10 Metrics Path Cardinality

| Option | Description | Pros | Cons |
|--------|-------------|------|------|
| **Normalized path (chosen)** | `/product/123` → `/product/{id}` | Low cardinality | Loses per-ID breakdown |
| **Raw path** | Actual path | Accurate | High cardinality; metric explosion |
| **Route template from mux** | Use Gorilla mux route name | Precise | Router integration needed |

**Chosen:** Normalized path.

**Why:** Keeps metric cardinality low for `/product/{id}` and `/admin/product/{id}`, suitable for request rate and latency by endpoint.

---

# 4. Go Patterns by Module

## 4.1 Handlers (`internal/handlers/`)

| Pattern | Usage |
|---------|-------|
| **Decode → Validate → Service → Encode** | Order: JSON decode, validate items, call `PlaceOrder`, map errors to HTTP codes |
| **Context propagation** | `r.Context()` passed to services for cancellation |
| **Shared error writer** | `writeError(w, status, message)` for uniform JSON error responses |
| **Mux Vars** | `mux.Vars(r)["productId"]`, `mux.Vars(r)["code"]` for path parameters |
| **Admin handler split** | `AdminProductHandler`, `AdminInventoryHandler`, `AdminCouponHandler` — each with focused responsibility |

## 4.2 Order Service (`internal/services/order.go`)

| Pattern | Usage |
|---------|-------|
| **Retry loop** | Up to 4 attempts on `InsufficientStockError` with exponential backoff + jitter |
| **Single transaction** | `tx.Begin`; idempotency, product existence, inventory, coupon, order, outbox, then `Commit` |
| **MaxUses resolution** | `getMaxUses(ctx, code)`: checks `CouponLimitsRepo` (DB) first; falls back to `CouponLimitsConfig` (env/file) |
| **Sentinel + custom errors** | `ErrEmptyOrder`, `ProductNotFoundError`, `InsufficientStockError`, `ErrCouponLimitExceeded` |
| **Deterministic order** | `sort.Strings(productIDs)` for consistent inventory reserve order |

## 4.3 Repositories (`internal/repository/`)

| Pattern | Usage |
|---------|-------|
| **Minimal Tx interfaces** | `Tx`, `CouponTx`, `OrderTx`, `ProductQueryExecer` with only required methods |
| **Interface-based design** | `ProductStore` for cache abstraction; `Publisher` for outbox |
| **Fallback behavior** | ProductCache: Redis failure → DB; GetAll/GetByID both fall back |
| **CouponLimitsRepo** | `GetMaxUses`, `SetLimit`, `GetAll`; DB limits override config; used by OrderService and AdminCouponHandler |
| **CouponUsageRepo** | `GetAll`, `ResetUsage` for admin; `CheckAndIncrement` for order flow |

## 4.4 Coupon Validator (`pkg/coupon/`)

| Pattern | Usage |
|---------|-------|
| **sync.RWMutex** | Protects content swap; readers hold `RLock` during validation |
| **Atomic swap** | `LoadContent()` builds new slice, replaces under `Lock` |
| **Background goroutine** | `StartBackgroundLoader` with `context.Context` for cancellation |
| **Case-insensitive** | `bytes.ToUpper` on content at load; `strings.ToUpper` on input |

## 4.5 Outbox (`internal/outbox/`)

| Pattern | Usage |
|---------|-------|
| **Abstract Publisher** | Interface; Redis and Kafka adapters |
| **Worker with context** | `Run(ctx)`; stops when context is cancelled |
| **Retry for failed** | `ResetFailedForRetry`; `StaleResetMinutes` for stuck “processing” |
| **FOR UPDATE SKIP LOCKED** | Claim pending rows without blocking other workers |

## 4.6 Main & Lifecycle (`cmd/api/main.go`)

| Pattern | Usage |
|---------|-------|
| **Graceful shutdown** | `signal.Notify(SIGTERM, SIGINT)` → cancel workers → `server.Shutdown(30s)` |
| **Context for workers** | Outbox, idempotency cleanup, coupon reload, stale reset share parent `ctx` |
| **Defer cleanup** | `defer db.Close()`, `defer publisher.Close()`, etc. |
| **Config from env** | All behavior configurable via environment variables |

---

# 5. Configuration & Hooks

## 5.1 Environment Variables (Configuration Hooks)

| Category | Variables | Purpose |
|----------|-----------|---------|
| **Database** | `DATABASE_URL`, `DB_POOL_MIN_CONNS`, `DB_POOL_MAX_CONNS`, `DATABASE_READ_REPLICA_URL` | Connection; pool size; read replica for product reads |
| **Redis** | `REDIS_URL` | Product cache; outbox (when broker=redis) |
| **Auth** | `API_KEY`, `ADMIN_API_KEY`, `METRICS_AUTH_TOKEN` | API and metrics auth |
| **Coupon** | `COUPON_MAX_USES`, `COUPON_LIMITS_JSON`, `COUPON_LIMITS_FILE`, `COUPON_RELOAD_INTERVAL` | Usage limits (fallback when not in DB); reload interval |
| **Outbox** | `OUTBOX_BROKER`, `OUTBOX_STREAM`, `KAFKA_BROKERS`, `KAFKA_OUTBOX_TOPIC` | Redis vs Kafka; broker settings |
| **Idempotency** | `IDEMPOTENCY_TTL_HOURS`, `IDEMPOTENCY_STALE_PROCESSING_MINUTES` | TTL and stuck-key reset |
| **Rate limit** | `RATE_LIMIT_RPS` | Global request throttling |
| **CORS** | `CORS_ORIGINS` | Allowed origins |
| **Observability** | `LOG_JSON`, `PORT` | Log format; listen port |

## 5.2 Extension Points (Code Hooks)

| Hook | Location | Purpose |
|------|----------|---------|
| **Publisher interface** | `internal/outbox/publisher.go` | Add new brokers (e.g. RabbitMQ, SQS) |
| **ProductStore interface** | `internal/repository/product_cache.go` | Swap cache implementation |
| **ProductDBRepo / ProductDBRepoWithReplica** | `internal/repository/product_db.go` | Single or dual pool; when `DATABASE_READ_REPLICA_URL` set, GetByID/GetAll use replica |
| **ProductQueryExecer** | `internal/repository/product_db.go` | Minimal tx interface for `ExistAllInTx` |
| **ExemptPaths (rate limit)** | `internal/middleware/ratelimit.go` | Add paths exempt from rate limiting |
| **CouponLimitsRepo** | `internal/repository/coupon_limits.go` | Admin-set limits; DB overrides config |
| **Worker config** | `outbox.WorkerConfig` | Poll interval, batch size, retry settings |
| **Coupon file paths** | `NewValidator(filePaths)` | Custom file paths beyond data dir |

---

# 6. Scaling to 10,000 Requests/Second

## 6.1 Bottleneck Analysis

| Component | Behavior | At 10k req/s |
|-----------|----------|--------------|
| **Product store** | Redis O(1) per key; DB fallback on failure | Suitable |
| **Coupon validator** | In-memory `bytes.Contains` over 3 slices | Low cost |
| **Order service** | Optimistic inventory; retry on conflict | Connection pool; less contention than row locks |
| **Single process** | Stateless; goroutines per request | Horizontal scaling behind LB |

## 6.2 Horizontal Scaling

- **Stateless design:** No in-process session state; any instance can serve any request.
- **Shared Redis:** Product cache shared across instances.
- **Shared PostgreSQL:** Inventory, orders, idempotency, outbox.
- **Load balancer:** Health probes (`/health/live`, `/health/ready`), round-robin or least-connections.

## 6.3 Go-Specific Scaling Behaviors

| Aspect | Behavior |
|--------|----------|
| **Goroutines** | ~10k concurrent requests ≈ 10k goroutines; Go handles this well |
| **Connection pool** | pgxpool; `DB_POOL_MIN_CONNS`, `DB_POOL_MAX_CONNS` limit connections per instance |
| **Read replica** | `DATABASE_READ_REPLICA_URL` routes product reads to replica; writes stay on primary |
| **No row locks** | Optimistic inventory avoids serialization on hot products |
| **Rate limit** | Global token bucket; `/health/*`, `/metrics` exempt for LB/orchestrator |
| **GOMAXPROCS** | Can be set to match CPU cores for better scheduling |

## 6.4 Recommended Deployment for 10k req/s

```
                    ┌──────────────────┐
                    │  Load Balancer   │
                    │  (health checks) │
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
                    │  (primary +    │
                    │   read replica)│
                    └────────┬────────┘
                             │
                    ┌────────▼────────┐
                    │  Redis          │
                    │  (product cache)│
                    └─────────────────┘
```

- **API nodes:** 3+ instances; each with connection pool and Redis client.
- **PostgreSQL:** Primary for writes; read replica for product reads when configured.
- **Redis:** Single instance or cluster; product cache shared.
- **Connection pools:** Tune `DB_POOL_MAX_CONNS` per instance; total &lt; DB `max_connections`.

---

# 7. Operational Hooks

## 7.1 Startup Hooks

| Hook | Purpose |
|------|---------|
| `CheckFilesExist()` | Coupon files present; logs warning if missing |
| `LoadContent()` | Coupon files loaded into memory |
| `productCache.GetAll(ctx)` | Cold-start Redis from DB |
| `db.SeedProductsFromJSON`, `SeedInventory` | Initial data load |

## 7.2 Background Workers

| Worker | Trigger | Config |
|--------|---------|--------|
| **Outbox publisher** | Poll every 2s | `WorkerConfig.PollInterval`, `BatchSize` |
| **Idempotency TTL cleanup** | Hourly | `IDEMPOTENCY_TTL_HOURS` |
| **Stale idempotency reset** | Every 5 min | `IDEMPOTENCY_STALE_PROCESSING_MINUTES` |
| **Coupon reload** | Every 5 min (default) | `COUPON_RELOAD_INTERVAL` |

## 7.3 Health & Observability

| Endpoint | Purpose | Auth |
|----------|---------|------|
| `/health/live` | Process alive | None |
| `/health/ready` | DB + Redis reachable | None |
| `/metrics` | Prometheus metrics | Optional `METRICS_AUTH_TOKEN` |

## 7.4 Graceful Shutdown

1. `SIGTERM` or `SIGINT` received.
2. Cancel outbox worker, idempotency cleanup, coupon reload, stale reset.
3. `server.Shutdown(ctx)` with 30s timeout.
4. Existing requests complete; new connections rejected.

---

# Appendix A: Module Map

| Module | Path | Responsibility |
|--------|------|----------------|
| Bootstrap | `cmd/api/main.go` | Wire dependencies; start server; signal handling |
| Models | `internal/models/` | Request/response structs |
| Handlers | `internal/handlers/` | HTTP handlers; validation; error mapping |
| Admin Product | `internal/handlers/admin.go` | Create/update/patch/delete products |
| Admin Inventory | `internal/handlers/admin_inventory.go` | GET/PUT inventory |
| Admin Coupon | `internal/handlers/admin_coupon.go` | GET coupons; PUT limit; PUT reset |
| Order Service | `internal/services/order.go` | Order flow; retry; transaction orchestration |
| Product Cache | `internal/repository/product_cache.go` | Redis-first cache; DB fallback |
| Product DB | `internal/repository/product_db.go` | CRUD; ExistAllInTx |
| Inventory | `internal/repository/inventory.go` | ReserveOptimistic; GetAll; SetQuantity |
| Order Repo | `internal/repository/order.go` | Order persistence |
| Coupon Usage | `internal/repository/coupon_usage.go` | CheckAndIncrement; GetAll; ResetUsage |
| Coupon Limits | `internal/repository/coupon_limits.go` | GetMaxUses; SetLimit; GetAll (DB overrides config) |
| Idempotency | `internal/repository/idempotency.go` | Reserve; Get; Complete; TTL; stale reset |
| Coupon Validator | `pkg/coupon/` | In-memory validation; background reload |
| Outbox | `internal/outbox/` | Repository; Publisher; worker |
| Config | `internal/config/` | Coupon limits from env/file (fallback) |
| Middleware | `internal/middleware/` | Auth; rate limit |
| Health | `internal/health/` | Liveness; readiness |
| Observability | `internal/observability/` | Metrics; logging |

---

# Appendix B: Key Interfaces

```go
// ProductStore — used by handlers and OrderService
type ProductStore interface {
    GetByID(ctx context.Context, id string) (*models.Product, error)
    GetAll(ctx context.Context) ([]models.Product, error)
}

// Publisher — outbox event publishing
type Publisher interface {
    Publish(ctx context.Context, ev *EventToPublish) error
    Close() error
}

// ProductQueryExecer — minimal tx for ExistAllInTx
type ProductQueryExecer interface {
    QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}
```

---

*This architecture document complements IMPLEMENTATION.md, which provides step-by-step flows and implementation details.*
