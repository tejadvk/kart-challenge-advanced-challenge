import { useState, useEffect, useCallback } from 'react'
import { Link } from 'react-router-dom'
import { fetchProducts, placeOrder } from './api'
import { calculateTotals } from './utils/discount'
import type { Product, CartItem, Order } from './types'
import { ProductCard } from './components/ProductCard'
import { Cart } from './components/Cart'
import { OrderConfirmation } from './components/OrderConfirmation'
import styles from './App.module.css'

function getQuantity(items: CartItem[], productId: string): number {
  return items.find((i) => i.product.id === productId)?.quantity ?? 0
}

export default function App() {
  const [products, setProducts] = useState<Product[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [cartItems, setCartItems] = useState<CartItem[]>([])
  const [couponCode, setCouponCode] = useState('')
  const [confirmedOrder, setConfirmedOrder] = useState<Order | null>(null)
  const [isPlacing, setIsPlacing] = useState(false)
  const [placeError, setPlaceError] = useState<string | null>(null)

  useEffect(() => {
    fetchProducts()
      .then(setProducts)
      .catch((err) => setError(err instanceof Error ? err.message : 'Failed to load products'))
      .finally(() => setLoading(false))
  }, [])

  const addToCart = useCallback((product: Product) => {
    setCartItems((prev) => {
      const existing = prev.find((i) => i.product.id === product.id)
      if (existing) {
        return prev.map((i) =>
          i.product.id === product.id ? { ...i, quantity: i.quantity + 1 } : i
        )
      }
      return [...prev, { product, quantity: 1 }]
    })
  }, [])

  const increaseQuantity = useCallback((productId: string) => {
    setCartItems((prev) =>
      prev.map((i) =>
        i.product.id === productId ? { ...i, quantity: i.quantity + 1 } : i
      )
    )
  }, [])

  const decreaseQuantity = useCallback((productId: string) => {
    setCartItems((prev) =>
      prev
        .map((i) =>
          i.product.id === productId ? { ...i, quantity: i.quantity - 1 } : i
        )
        .filter((i) => i.quantity > 0)
    )
  }, [])

  const removeFromCart = useCallback((productId: string) => {
    setCartItems((prev) => prev.filter((i) => i.product.id !== productId))
  }, [])

  const handleConfirmOrder = useCallback(async () => {
    if (cartItems.length === 0) return
    setIsPlacing(true)
    setPlaceError(null)
    try {
      const order = await placeOrder({
        items: cartItems.map((i) => ({
          productId: i.product.id,
          quantity: i.quantity,
        })),
        couponCode: couponCode.trim() || undefined,
      })
      setConfirmedOrder(order)
      setCartItems([])
      setCouponCode('')
    } catch (err) {
      setPlaceError(err instanceof Error ? err.message : 'Failed to place order')
    } finally {
      setIsPlacing(false)
    }
  }, [cartItems, couponCode])

  const handleStartNewOrder = useCallback(() => {
    setConfirmedOrder(null)
  }, [])

  const { subtotal, discount, total } = calculateTotals(cartItems, couponCode)

  if (loading) {
    return (
      <div className={styles.loading}>
        <p>Loading products…</p>
      </div>
    )
  }

  if (error) {
    return (
      <div className={styles.error}>
        <p>{error}</p>
      </div>
    )
  }

  const displayProducts = products

  return (
    <div className={styles.app}>
      <header className={styles.header}>
        <h1 className={styles.pageTitle}>Desserts</h1>
        <Link to="/admin" className={styles.adminLink}>Admin</Link>
      </header>

      <main className={styles.main}>
        <section className={styles.products} aria-label="Product catalog">
          <div className={styles.grid}>
            {displayProducts.map((product) => (
              <ProductCard
                key={product.id}
                product={product}
                quantity={getQuantity(cartItems, product.id)}
                onAdd={() => addToCart(product)}
                onIncrease={() => increaseQuantity(product.id)}
                onDecrease={() => decreaseQuantity(product.id)}
              />
            ))}
          </div>
        </section>

        <div className={styles.cartWrapper}>
        <Cart
          items={cartItems}
          couponCode={couponCode}
          onCouponChange={setCouponCode}
          onRemove={removeFromCart}
          onConfirm={handleConfirmOrder}
          subtotal={subtotal}
          total={total}
          discount={discount > 0 ? discount : undefined}
          isPlacing={isPlacing}
        />
        </div>
      </main>

      {placeError && (
        <div className={styles.toast} role="alert">
          {placeError}
        </div>
      )}

      {confirmedOrder && (
        <OrderConfirmation order={confirmedOrder} onStartNew={handleStartNewOrder} />
      )}
    </div>
  )
}
