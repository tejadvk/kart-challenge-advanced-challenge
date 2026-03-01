import type { CartItem } from '../types'
import { EmptyCart } from './EmptyCart'
import styles from './Cart.module.css'

interface CartProps {
  items: CartItem[]
  couponCode: string
  onCouponChange: (value: string) => void
  onRemove: (productId: string) => void
  onConfirm: () => void
  subtotal: number
  total: number
  discount?: number
  isPlacing: boolean
}

export function Cart({
  items,
  couponCode,
  onCouponChange,
  onRemove,
  onConfirm,
  subtotal,
  total,
  discount,
  isPlacing,
}: CartProps) {
  const itemCount = items.reduce((sum, i) => sum + i.quantity, 0)

  return (
    <aside className={styles.cart}>
      <h2 className={styles.title}>Your Cart ({itemCount})</h2>
      <div className={styles.content}>
        {items.length === 0 ? (
          <EmptyCart />
        ) : (
          <>
            <ul className={styles.list}>
              {items.map(({ product, quantity }) => (
                <li key={product.id} className={styles.item}>
                  <div className={styles.itemMain}>
                    <div className={styles.itemInfo}>
                      <span className={styles.itemName}>{product.name}</span>
                      <span className={styles.itemMeta}>
                        <span className={styles.qty}>{quantity}x</span> @ ${product.price.toFixed(2)}{' '}
                        <span className={styles.itemTotal}>${(product.price * quantity).toFixed(2)}</span>
                      </span>
                    </div>
                    <button
                      type="button"
                      className={styles.removeBtn}
                      onClick={() => onRemove(product.id)}
                      aria-label={`Remove ${product.name} from cart`}
                    >
                      <span aria-hidden>×</span>
                    </button>
                  </div>
                </li>
              ))}
            </ul>
            <div className={styles.totalRow}>
              <span>Order Total</span>
              <span className={styles.totalValue}>${total.toFixed(2)}</span>
            </div>
            {discount != null && discount > 0 && (
              <p className={styles.discountNote}>
                Subtotal ${subtotal.toFixed(2)} − ${discount.toFixed(2)} discount
              </p>
            )}
          </>
        )}
        {items.length > 0 && (
          <>
            <div className={styles.couponRow}>
              <label htmlFor="coupon-code" className={styles.couponLabel}>
                Discount code
              </label>
              <input
                id="coupon-code"
                type="text"
                className={styles.couponInput}
                placeholder="Enter code"
                value={couponCode}
                onChange={(e) => onCouponChange(e.target.value)}
                aria-label="Discount code"
              />
            </div>
            <p className={styles.carbonNote}>
              <span className={styles.leaf} aria-hidden>🌿</span>
              This is a carbon-neutral delivery
            </p>
            <button
              type="button"
              className={styles.confirmBtn}
              onClick={onConfirm}
              disabled={isPlacing}
              aria-busy={isPlacing}
            >
              {isPlacing ? 'Placing order…' : 'Confirm Order'}
            </button>
          </>
        )}
      </div>
    </aside>
  )
}
