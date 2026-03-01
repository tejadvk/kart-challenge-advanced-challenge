import type { Order } from '../types'
import styles from './OrderConfirmation.module.css'

interface OrderConfirmationProps {
  order: Order
  onStartNew: () => void
}

export function OrderConfirmation({ order, onStartNew }: OrderConfirmationProps) {
  const products = order.products || []
  const itemsWithProducts = (order.items || []).map((item) => {
    const product = products.find((p) => p.id === item.productId)
    return { ...item, product }
  }).filter((i) => i.product)

  return (
    <div className={styles.backdrop} role="dialog" aria-labelledby="order-confirmed-title" aria-modal="true">
      <div className={styles.modal}>
        <div className={styles.header}>
          <div className={styles.checkmark} aria-hidden>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
              <circle cx="12" cy="12" r="10" />
              <path d="M8 12l3 3 5-6" />
            </svg>
          </div>
          <h2 id="order-confirmed-title" className={styles.title}>Order Confirmed</h2>
          <p className={styles.subtitle}>We hope you enjoy your food!</p>
        </div>
        <div className={styles.summary}>
          <ul className={styles.list}>
            {itemsWithProducts.map(({ productId, quantity, product }) => {
              if (!product) return null
              const img = product.image?.thumbnail || product.image?.mobile
              return (
                <li key={productId} className={styles.item}>
                  {img && (
                    <img src={img} alt="" className={styles.thumb} />
                  )}
                  <div className={styles.itemInfo}>
                    <span className={styles.itemName}>{product.name}</span>
                    <span className={styles.itemMeta}>
                      <span className={styles.qty}>{quantity}x</span> @ ${product.price.toFixed(2)}
                    </span>
                  </div>
                  <span className={styles.itemTotal}>${(product.price * quantity).toFixed(2)}</span>
                </li>
              )
            })}
          </ul>
          <div className={styles.totalRow}>
            <span>Order Total</span>
            <span className={styles.totalValue}>${order.total.toFixed(2)}</span>
          </div>
        </div>
        <button
          type="button"
          className={styles.startNewBtn}
          onClick={onStartNew}
        >
          Start New Order
        </button>
      </div>
    </div>
  )
}
