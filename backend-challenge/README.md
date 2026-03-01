# Food Ordering API - Backend Challenge

Go implementation of the food ordering API per the [OpenAPI specification](../api/openapi.yaml).

Implements product listing, order placement with inventory management, and coupon validation.

## Prerequisites

- Go 1.21+
- PostgreSQL 14+ (or use Docker)

## Ports

| Context        | Backend  | PostgreSQL | Redis |
|----------------|----------|------------|-------|
| Local (`go run`)| 8080     | 5432       | 6379  |
| Docker (full stack) | 8081 | 5432   | 6379  |

## Quick Start with Docker

**Option A – Backend services only (run API on host):**
```bash
# Start PostgreSQL and Redis
docker compose up -d postgres redis

# Wait for services (~5 seconds), then run the API
go run ./cmd/api
```

**Option B – Full stack (from project root):**
```bash
docker compose up -d
# Frontend: http://localhost:3000  |  Backend: http://localhost:8081
```

**Option C – Backend API in Docker (from project root):**
```bash
cd ..
docker compose up -d postgres redis backend
```

## Manual Setup

1. Create a PostgreSQL database named `kartchallenge`:
   ```bash
   createdb kartchallenge
   ```

2. Set the connection string (optional; defaults to local):
   ```bash
   export DATABASE_URL="postgres://user:password@localhost:5432/kartchallenge?sslmode=disable"
   ```

3. Run the API:
   ```bash
   go run ./cmd/api
   ```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DATABASE_URL` | `postgres://postgres:postgres@localhost:5432/kartchallenge?sslmode=disable` | PostgreSQL connection string |
| `REDIS_URL` | `redis://localhost:6379/0` | Redis connection string |
| `PORT` | `8080` | HTTP server port |
| `INITIAL_INVENTORY` | `100` | Initial stock per product for seeding |
| `ADMIN_API_KEY` | `admin` | API key for admin endpoints |
| `COUPON_MAX_USES` | `0` (unlimited) | Default usage limit per coupon |
| `COUPON_LIMITS_JSON` | - | Per-code limits, e.g. `{"HAPPYHOURS":50}` |
| `COUPON_LIMITS_FILE` | - | Path to JSON file with per-code limits |

## API Endpoints

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | /product | No | List all products (from Redis cache) |
| GET | /product/{id} | No | Get product by ID |
| POST | /order | Yes (`api_key` header) | Place order |
| POST | /admin/product | Yes (`admin_api_key` header) | Create product |
| PUT | /admin/product/{id} | Yes (`admin_api_key`) | Update product |
| PATCH | /admin/product/{id} | Yes (`admin_api_key`) | Partial update (e.g. price) |
| DELETE | /admin/product/{id} | Yes (`admin_api_key`) | Delete product |
| GET | /admin/inventory | Yes (`admin_api_key`) | List all inventory (productId, quantity) |
| PUT | /admin/inventory/{productId} | Yes (`admin_api_key`) | Set quantity (body: `{"quantity": N}`) |
| GET | /admin/coupons | Yes (`admin_api_key`) | List coupon usage & limits |
| PUT | /admin/coupons/{code}/limit | Yes (`admin_api_key`) | Set usage limit (body: `{"maxUses": N}`, 0 = unlimited) |
| PUT | /admin/coupons/{code}/reset | Yes (`admin_api_key`) | Reset used count to 0 |

- **API key:** `apitest` in the `api_key` header
- **Idempotency (optional):** Send `Idempotency-Key` or `X-Idempotency-Key` with a unique value (e.g. UUID) to prevent duplicate orders on retries

## Phase 3: Database & Inventory

- **PostgreSQL** stores `inventory` and `orders` / `order_items`
- **SELECT FOR UPDATE** prevents overbooking under concurrent requests
- Returns **409 Conflict** when stock is insufficient

## Phase 4: Coupon Limits & Idempotency

- **Coupon usage limits:** Configurable via env (`COUPON_MAX_USES`) or per-code file (`COUPON_LIMITS_FILE`, `COUPON_LIMITS_JSON`). Admin can also set limits via `PUT /admin/coupons/{code}/limit` (persisted in `coupon_limits` table).
- **Idempotency:** Send `Idempotency-Key` header to avoid duplicate orders on retries

## Products: DB + Redis + Admin API

- **Products** stored in PostgreSQL, served from **Redis** (distributed cache)
- **Delta updates:** Admin API updates only changed products in Redis (no full cache refresh)
- **Admin API:** Create, update, patch, delete products via `/admin/product*`

See [IMPLEMENTATION.md](./IMPLEMENTATION.md) for full documentation.
