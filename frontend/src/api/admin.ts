import type { Product } from '../types'

const API_BASE = import.meta.env.VITE_API_URL || 'https://orderfoodonline.deno.dev/api'
const ADMIN_KEY = import.meta.env.VITE_ADMIN_API_KEY || 'admin'

function adminHeaders(): Record<string, string> {
  return {
    'Content-Type': 'application/json',
    admin_api_key: ADMIN_KEY,
  }
}

export interface InventoryItem {
  productId: string
  quantity: number
}

export async function adminCreateProduct(product: Product): Promise<Product> {
  const res = await fetch(`${API_BASE}/admin/product`, {
    method: 'POST',
    headers: adminHeaders(),
    body: JSON.stringify(product),
  })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) throw new Error(data.message || `Create failed: ${res.status}`)
  return data
}

export async function adminUpdateProduct(id: string, product: Product): Promise<Product> {
  const res = await fetch(`${API_BASE}/admin/product/${id}`, {
    method: 'PUT',
    headers: adminHeaders(),
    body: JSON.stringify({ ...product, id }),
  })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) throw new Error(data.message || `Update failed: ${res.status}`)
  return data
}

export async function adminPatchProduct(
  id: string,
  patch: Partial<Pick<Product, 'name' | 'price' | 'category' | 'image'>>
): Promise<Product> {
  const res = await fetch(`${API_BASE}/admin/product/${id}`, {
    method: 'PATCH',
    headers: adminHeaders(),
    body: JSON.stringify(patch),
  })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) throw new Error(data.message || `Patch failed: ${res.status}`)
  return data
}

export async function adminDeleteProduct(id: string): Promise<void> {
  const res = await fetch(`${API_BASE}/admin/product/${id}`, {
    method: 'DELETE',
    headers: adminHeaders(),
  })
  if (!res.ok) {
    const data = await res.json().catch(() => ({}))
    throw new Error(data.message || `Delete failed: ${res.status}`)
  }
}

export async function adminListInventory(): Promise<InventoryItem[]> {
  const res = await fetch(`${API_BASE}/admin/inventory`, {
    headers: adminHeaders(),
  })
  if (!res.ok) throw new Error('Failed to fetch inventory')
  return res.json()
}

export async function adminUpdateInventory(productId: string, quantity: number): Promise<InventoryItem> {
  const res = await fetch(`${API_BASE}/admin/inventory/${productId}`, {
    method: 'PUT',
    headers: adminHeaders(),
    body: JSON.stringify({ quantity }),
  })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) throw new Error(data.message || `Update inventory failed: ${res.status}`)
  return data
}

export interface CouponItem {
  couponCode: string
  usedCount: number
  maxUses?: number
}

export async function adminListCoupons(): Promise<CouponItem[]> {
  const res = await fetch(`${API_BASE}/admin/coupons`, {
    headers: adminHeaders(),
  })
  if (!res.ok) throw new Error('Failed to fetch coupons')
  const data = await res.json()
  return Array.isArray(data) ? data : []
}

export async function adminSetCouponLimit(code: string, maxUses: number): Promise<CouponItem> {
  const res = await fetch(`${API_BASE}/admin/coupons/${encodeURIComponent(code)}/limit`, {
    method: 'PUT',
    headers: adminHeaders(),
    body: JSON.stringify({ maxUses }),
  })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) throw new Error(data.message || `Set coupon limit failed: ${res.status}`)
  return data
}

export async function adminResetCouponUsage(code: string): Promise<void> {
  const res = await fetch(`${API_BASE}/admin/coupons/${encodeURIComponent(code)}/reset`, {
    method: 'PUT',
    headers: adminHeaders(),
  })
  if (!res.ok) {
    const data = await res.json().catch(() => ({}))
    throw new Error(data.message || `Reset coupon usage failed: ${res.status}`)
  }
}
