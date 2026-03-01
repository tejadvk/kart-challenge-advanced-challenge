# Food Ordering – Frontend

A responsive React shopping cart for the food ordering challenge. Built with Vite, React 18, and TypeScript.

## Features

- **Product catalog** – Grid of products with images, names, prices
- **Admin page** (`/admin`) – Manage products (create, edit, delete), inventory (set quantity), and coupons (view usage, set limits, reset usage)
- **Cart** – Add/remove items, change quantity
- **Order total** – Correct calculation including discounts
- **Discount codes**
  - `HAPPYHOURS` or `HAPPYHRS` – 18% off
  - `BUYGETONE` – Lowest priced item free
- **Order confirmation** – Modal after successful order
- **Responsive** – Mobile (375px) to desktop (1440px+)
- **Accessibility** – Focus states, ARIA labels, WCAG-friendly

## Design

- **Font:** Red Hat Text (400, 600, 700)
- **Product names:** 16px
- **Accent:** Reddish-orange (#e85d4c)
- **Layout:** Product grid + cart sidebar on desktop; stacked on mobile

## Getting Started

```bash
cd frontend
npm install
npm run dev
```

Open [http://localhost:5173](http://localhost:5173).

## Ports

| Context      | Frontend        | Backend  |
|--------------|-----------------|----------|
| Local dev    | http://localhost:5173 (Vite) | http://localhost:8080 |
| Docker       | http://localhost:3000        | http://localhost:8081 |

## Environment

| Variable       | Default                              | Description              |
|----------------|--------------------------------------|--------------------------|
| `VITE_API_URL` | `https://orderfoodonline.deno.dev/api` | API base URL             |
| `VITE_API_KEY` | `apitest`                            | API key for POST /order  |
| `VITE_ADMIN_API_KEY` | `admin`                        | Admin API key for /admin/* (products, inventory, coupons) |

**Note:** The admin page and inventory API require the local backend. The demo API (orderfoodonline.deno.dev) does not expose admin endpoints.

**Local backend** (`.env.local`):
```
VITE_API_URL=http://localhost:8080
VITE_API_KEY=apitest
VITE_ADMIN_API_KEY=admin
```

**Docker** (frontend is built with `VITE_API_URL=http://localhost:8081` in docker-compose).

**Vite proxy** (optional): Set `VITE_API_URL=/api`; requests to `/api/*` proxy to `localhost:8080`.

## Build

```bash
npm run build
npm run preview
```

## Docker

From the **project root**:

```bash
docker compose up -d
```

- Frontend: http://localhost:3000
- Backend: http://localhost:8081 (frontend is built with this URL)

Or build the frontend image only (requires `VITE_API_URL` for backend URL):
```bash
docker build -t kart-frontend --build-arg VITE_API_URL=http://localhost:8081 .
docker run -p 3000:80 kart-frontend
```

## Project Structure

```
frontend/
├── src/
│   ├── api.ts           # Store API client
│   ├── api/admin.ts     # Admin API (products, inventory, coupons)
│   ├── types.ts         # TypeScript types
│   ├── utils/discount.ts
│   ├── components/
│   │   ├── ProductCard.tsx
│   │   ├── Cart.tsx
│   │   ├── EmptyCart.tsx
│   │   ├── OrderConfirmation.tsx
│   │   ├── ConfirmDialog.tsx
│   │   └── Toast.tsx
│   ├── pages/
│   │   ├── AdminPage.tsx    # Products, inventory, coupon management
│   │   └── StorePage.tsx
│   ├── App.tsx
│   └── main.tsx
├── index.html
└── package.json
```
