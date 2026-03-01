import type { Product, Order, OrderReq } from './types'

const API_BASE = import.meta.env.VITE_API_URL || 'https://orderfoodonline.deno.dev/api'
const API_KEY = import.meta.env.VITE_API_KEY || 'apitest'

export async function fetchProducts(): Promise<Product[]> {
  const res = await fetch(`${API_BASE}/product`)
  if (!res.ok) throw new Error('Failed to fetch products')
  return res.json()
}

export async function placeOrder(req: OrderReq): Promise<Order> {
  const res = await fetch(`${API_BASE}/order`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      api_key: API_KEY,
    },
    body: JSON.stringify(req),
  })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    throw new Error(data.message || `Order failed: ${res.status}`)
  }
  return data
}
