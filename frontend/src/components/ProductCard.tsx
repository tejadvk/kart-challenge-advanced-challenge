import type { Product } from '../types'
import { ProductImage } from './ProductImage'
import styles from './ProductCard.module.css'

interface ProductCardProps {
  product: Product
  quantity: number
  onAdd: () => void
  onIncrease: () => void
  onDecrease: () => void
}

export function ProductCard({ product, quantity, onAdd, onIncrease, onDecrease }: ProductCardProps) {
  return (
    <article className={styles.card}>
      <div className={styles.imageWrapper}>
        <ProductImage image={product.image} alt={product.name} className={styles.image} />
        {!product.image && (
          <div className={styles.imagePlaceholder} aria-hidden>
            <span>🍽</span>
          </div>
        )}
      </div>
      <div className={styles.content}>
        {quantity === 0 ? (
          <button
            type="button"
            className={styles.addButton}
            onClick={onAdd}
            aria-label={`Add ${product.name} to cart`}
          >
            <span className={styles.cartIcon} aria-hidden>🛒</span>
            Add to Cart
          </button>
        ) : (
          <div className={styles.quantityControl} role="group" aria-label={`Quantity of ${product.name}`}>
            <button
              type="button"
              className={styles.qtyBtn}
              onClick={onDecrease}
              aria-label="Decrease quantity"
            >
              −
            </button>
            <span className={styles.qtyValue}>{quantity}</span>
            <button
              type="button"
              className={styles.qtyBtn}
              onClick={onIncrease}
              aria-label="Increase quantity"
            >
              +
            </button>
          </div>
        )}
        <p className={styles.category}>{product.category}</p>
        <h3 className={styles.name}>{product.name}</h3>
        <p className={styles.price}>${product.price.toFixed(2)}</p>
      </div>
    </article>
  )
}
