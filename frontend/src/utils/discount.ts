import type { CartItem } from '../types'

export function calculateTotals(items: CartItem[], couponCode: string): {
  subtotal: number
  discount: number
  total: number
} {
  const subtotal = items.reduce((sum, i) => sum + i.product.price * i.quantity, 0)
  let discount = 0
  const code = couponCode.trim().toUpperCase()

  if (code === 'HAPPYHOURS' || code === 'HAPPYHRS') {
    discount = subtotal * 0.18
  } else if (code === 'BUYGETONE') {
    if (items.length > 0) {
      const cheapest = items.reduce((min, i) =>
        i.product.price < min.product.price ? i : min
      )
      discount = cheapest.product.price * cheapest.quantity
    }
  }

  return {
    subtotal,
    discount,
    total: Math.max(0, subtotal - discount),
  }
}
