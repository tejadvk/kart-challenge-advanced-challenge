import { useState, useEffect, useCallback } from 'react'
import { Link } from 'react-router-dom'
import { fetchProducts } from '../api'
import {
  adminCreateProduct,
  adminUpdateProduct,
  adminDeleteProduct,
  adminListInventory,
  adminUpdateInventory,
  adminListCoupons,
  adminSetCouponLimit,
  adminResetCouponUsage,
  type InventoryItem,
  type CouponItem,
} from '../api/admin'
import type { Product } from '../types'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { Toast } from '../components/Toast'
import styles from './AdminPage.module.css'

type PendingAction =
  | { type: 'create'; product: Product }
  | { type: 'update'; product: Product }
  | { type: 'delete'; productId: string; productName: string }
  | { type: 'inventory'; productId: string; productName: string; quantity: number }
  | { type: 'couponLimit'; couponCode: string; maxUses: number }
  | { type: 'couponReset'; couponCode: string }

type ToastState = { type: 'success' | 'error'; message: string } | null

const emptyImage = {
  thumbnail: '',
  mobile: '',
  tablet: '',
  desktop: '',
}

export function AdminPage() {
  const [products, setProducts] = useState<Product[]>([])
  const [inventory, setInventory] = useState<Record<string, number>>({})
  const [coupons, setCoupons] = useState<CouponItem[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [editingProduct, setEditingProduct] = useState<Product | null>(null)
  const [creating, setCreating] = useState(false)
  const [inventoryEdits, setInventoryEdits] = useState<Record<string, string>>({})
  const [couponLimitEdits, setCouponLimitEdits] = useState<Record<string, string>>({})
  const [addingCouponLimit, setAddingCouponLimit] = useState(false)
  const [newCouponCode, setNewCouponCode] = useState('')
  const [newCouponMaxUses, setNewCouponMaxUses] = useState('')
  const [pendingAction, setPendingAction] = useState<PendingAction | null>(null)
  const [toast, setToast] = useState<ToastState>(null)

  const loadData = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const [prods, inv, coup] = await Promise.all([
        fetchProducts(),
        adminListInventory(),
        adminListCoupons(),
      ])
      setProducts(prods)
      setCoupons(coup)
      const invMap: Record<string, number> = {}
      inv.forEach((i: InventoryItem) => {
        invMap[i.productId] = i.quantity
      })
      prods.forEach((p) => {
        if (!(p.id in invMap)) invMap[p.id] = 0
      })
      setInventory(invMap)
      const edits: Record<string, string> = {}
      prods.forEach((p) => {
        edits[p.id] = String(invMap[p.id] ?? 0)
      })
      setInventoryEdits(edits)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load data')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    loadData()
  }, [loadData])

  const showToast = useCallback((type: 'success' | 'error', message: string) => {
    setToast({ type, message })
  }, [])

  const handleCreateProductRequest = (product: Product) => {
    setPendingAction({ type: 'create', product })
  }

  const handleCreateProductConfirm = async () => {
    if (!pendingAction || pendingAction.type !== 'create') return
    const { product } = pendingAction
    setPendingAction(null)
    try {
      await adminCreateProduct(product)
      setCreating(false)
      loadData()
      showToast('success', `Product "${product.name}" created successfully.`)
    } catch (err) {
      showToast('error', err instanceof Error ? err.message : 'Failed to create product.')
    }
  }

  const handleUpdateProductRequest = (product: Product) => {
    setPendingAction({ type: 'update', product })
  }

  const handleUpdateProductConfirm = async () => {
    if (!pendingAction || pendingAction.type !== 'update') return
    const { product } = pendingAction
    setPendingAction(null)
    try {
      await adminUpdateProduct(product.id, product)
      setEditingProduct(null)
      loadData()
      showToast('success', `Product "${product.name}" updated successfully.`)
    } catch (err) {
      showToast('error', err instanceof Error ? err.message : 'Failed to update product.')
    }
  }

  const handleDeleteProduct = (id: string, productName: string) => {
    setPendingAction({ type: 'delete', productId: id, productName })
  }

  const handleDeleteProductConfirm = async () => {
    if (!pendingAction || pendingAction.type !== 'delete') return
    const { productId } = pendingAction
    setPendingAction(null)
    try {
      await adminDeleteProduct(productId)
      loadData()
      showToast('success', 'Product deleted successfully.')
    } catch (err) {
      showToast('error', err instanceof Error ? err.message : 'Failed to delete product.')
    }
  }

  const handleInventoryChange = (productId: string, value: string) => {
    setInventoryEdits((prev) => ({ ...prev, [productId]: value }))
  }

  const handleSaveInventoryRequest = (productId: string) => {
    const raw = inventoryEdits[productId] ?? String(inventory[productId] ?? 0)
    const qty = parseInt(raw, 10)
    if (isNaN(qty) || qty < 0) {
      setInventoryEdits((prev) => ({ ...prev, [productId]: String(inventory[productId] ?? 0) }))
      showToast('error', 'Please enter a valid quantity (0 or greater).')
      return
    }
    const product = products.find((p) => p.id === productId)
    setPendingAction({
      type: 'inventory',
      productId,
      productName: product?.name ?? productId,
      quantity: qty,
    })
  }

  const handleSaveInventoryConfirm = async () => {
    if (!pendingAction || pendingAction.type !== 'inventory') return
    const { productId, productName, quantity } = pendingAction
    setPendingAction(null)
    try {
      await adminUpdateInventory(productId, quantity)
      setInventory((prev) => ({ ...prev, [productId]: quantity }))
      showToast('success', `Inventory for "${productName}" updated to ${quantity}.`)
    } catch (err) {
      showToast('error', err instanceof Error ? err.message : 'Failed to update inventory.')
    }
  }

  const handleSetCouponLimitRequest = (code: string) => {
    const raw = couponLimitEdits[code] ?? String(coupons.find((c) => c.couponCode === code)?.maxUses ?? '')
    const maxUses = parseInt(raw, 10)
    if (isNaN(maxUses) || maxUses < 0) {
      showToast('error', 'Please enter a valid limit (0 = unlimited).')
      return
    }
    setPendingAction({ type: 'couponLimit', couponCode: code, maxUses })
  }

  const handleSetCouponLimitConfirm = async () => {
    if (!pendingAction || pendingAction.type !== 'couponLimit') return
    const { couponCode, maxUses } = pendingAction
    setPendingAction(null)
    try {
      await adminSetCouponLimit(couponCode, maxUses)
      loadData()
      showToast('success', `Limit for "${couponCode}" set to ${maxUses === 0 ? 'unlimited' : maxUses}.`)
    } catch (err) {
      showToast('error', err instanceof Error ? err.message : 'Failed to set coupon limit.')
    }
  }

  const handleResetCouponRequest = (code: string) => {
    setPendingAction({ type: 'couponReset', couponCode: code })
  }

  const handleResetCouponConfirm = async () => {
    if (!pendingAction || pendingAction.type !== 'couponReset') return
    const { couponCode } = pendingAction
    setPendingAction(null)
    try {
      await adminResetCouponUsage(couponCode)
      loadData()
      showToast('success', `Usage for "${couponCode}" reset to 0.`)
    } catch (err) {
      showToast('error', err instanceof Error ? err.message : 'Failed to reset coupon usage.')
    }
  }

  const handleAddCouponLimitSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    const code = newCouponCode.trim().toUpperCase()
    const maxUses = parseInt(newCouponMaxUses, 10)
    if (!code) {
      showToast('error', 'Enter a coupon code.')
      return
    }
    if (isNaN(maxUses) || maxUses < 0) {
      showToast('error', 'Enter a valid limit (0 = unlimited).')
      return
    }
    setPendingAction({ type: 'couponLimit', couponCode: code, maxUses })
  }

  const handleCouponLimitChange = (code: string, value: string) => {
    setCouponLimitEdits((prev) => ({ ...prev, [code]: value }))
  }

  if (loading) {
    return (
      <div className={styles.page}>
        <p className={styles.loading}>Loading…</p>
      </div>
    )
  }

  if (error) {
    return (
      <div className={styles.page}>
        <p className={styles.error}>{error}</p>
        <button type="button" onClick={loadData} className={styles.retryBtn}>
          Retry
        </button>
      </div>
    )
  }

  return (
    <div className={styles.page}>
      <header className={styles.header}>
        <h1 className={styles.title}>Admin</h1>
        <Link to="/" className={styles.link}>
          ← Back to Store
        </Link>
      </header>

      <main className={styles.main}>
        <section className={styles.section}>
          <div className={styles.sectionHeader}>
            <h2>Products</h2>
            {!creating && !editingProduct && (
              <button
                type="button"
                className={styles.primaryBtn}
                onClick={() => setCreating(true)}
              >
                Add Product
              </button>
            )}
          </div>

          {creating && (
            <ProductForm
              product={{
                id: '',
                name: '',
                category: '',
                price: 0,
                image: emptyImage,
              }}
              onSave={handleCreateProductRequest}
              onCancel={() => setCreating(false)}
            />
          )}

          {editingProduct && (
            <ProductForm
              product={editingProduct}
              onSave={handleUpdateProductRequest}
              onCancel={() => setEditingProduct(null)}
            />
          )}

          <div className={styles.tableWrap}>
            <table className={styles.table}>
              <thead>
                <tr>
                  <th>ID</th>
                  <th>Name</th>
                  <th>Category</th>
                  <th>Price</th>
                  <th>Actions</th>
                </tr>
              </thead>
              <tbody>
                {products.map((p) => (
                  <tr key={p.id}>
                    <td>{p.id}</td>
                    <td>{p.name}</td>
                    <td>{p.category}</td>
                    <td>${p.price.toFixed(2)}</td>
                    <td>
                      <button
                        type="button"
                        className={styles.smallBtn}
                        onClick={() => setEditingProduct(p)}
                      >
                        Edit
                      </button>
                      <button
                        type="button"
                        className={styles.dangerBtn}
                        onClick={() => handleDeleteProduct(p.id, p.name)}
                      >
                        Delete
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>

        <section className={styles.section}>
          <h2>Inventory</h2>
          <div className={styles.tableWrap}>
            <table className={styles.table}>
              <thead>
                <tr>
                  <th>Product ID</th>
                  <th>Product Name</th>
                  <th>Quantity</th>
                  <th>Actions</th>
                </tr>
              </thead>
              <tbody>
                {products.map((p) => (
                  <tr key={p.id}>
                    <td>{p.id}</td>
                    <td>{p.name}</td>
                    <td>
                      <input
                        type="number"
                        min={0}
                        value={inventoryEdits[p.id] ?? inventory[p.id] ?? 0}
                        onChange={(e) => handleInventoryChange(p.id, e.target.value)}
                        className={styles.qtyInput}
                      />
                    </td>
                    <td>
                      <button
                        type="button"
                        className={styles.smallBtn}
                        onClick={() => handleSaveInventoryRequest(p.id)}
                      >
                        Save
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {products.length === 0 && (
            <p className={styles.empty}>No products. Add a product first.</p>
          )}
        </section>

        <section className={styles.section}>
          <div className={styles.sectionHeader}>
            <h2>Coupons</h2>
            {!addingCouponLimit && (
              <button
                type="button"
                className={styles.primaryBtn}
                onClick={() => setAddingCouponLimit(true)}
              >
                Add coupon limit
              </button>
            )}
          </div>
          {addingCouponLimit && (
            <form className={styles.form} onSubmit={handleAddCouponLimitSubmit}>
              <div className={styles.formRow}>
                <label>
                  Coupon code
                  <input
                    type="text"
                    placeholder="e.g. HAPPYHOURS"
                    value={newCouponCode}
                    onChange={(e) => setNewCouponCode(e.target.value)}
                  />
                </label>
                <label>
                  Max uses (0 = unlimited)
                  <input
                    type="number"
                    min={0}
                    placeholder="0"
                    value={newCouponMaxUses}
                    onChange={(e) => setNewCouponMaxUses(e.target.value)}
                  />
                </label>
              </div>
              <div className={styles.formActions}>
                <button type="submit" className={styles.primaryBtn}>
                  Set limit
                </button>
                <button
                  type="button"
                  onClick={() => {
                    setAddingCouponLimit(false)
                    setNewCouponCode('')
                    setNewCouponMaxUses('')
                  }}
                  className={styles.secondaryBtn}
                >
                  Cancel
                </button>
              </div>
            </form>
          )}
          <div className={styles.tableWrap}>
            <table className={styles.table}>
              <thead>
                <tr>
                  <th>Code</th>
                  <th>Used</th>
                  <th>Limit (0 = unlimited)</th>
                  <th>Actions</th>
                </tr>
              </thead>
              <tbody>
                {coupons.map((c) => (
                  <tr key={c.couponCode}>
                    <td>{c.couponCode}</td>
                    <td>{c.usedCount}</td>
                    <td>
                      <input
                        type="number"
                        min={0}
                        value={couponLimitEdits[c.couponCode] ?? (c.maxUses != null ? String(c.maxUses) : '')}
                        onChange={(e) => handleCouponLimitChange(c.couponCode, e.target.value)}
                        placeholder="—"
                        className={styles.qtyInput}
                      />
                    </td>
                    <td>
                      <button
                        type="button"
                        className={styles.smallBtn}
                        onClick={() => handleSetCouponLimitRequest(c.couponCode)}
                      >
                        Set limit
                      </button>
                      <button
                        type="button"
                        className={styles.dangerBtn}
                        onClick={() => handleResetCouponRequest(c.couponCode)}
                        disabled={c.usedCount === 0}
                      >
                        Reset usage
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {coupons.length === 0 && (
            <p className={styles.empty}>No coupons with usage or limits yet. Add a limit above or use a coupon in an order.</p>
          )}
        </section>
      </main>

      {pendingAction?.type === 'create' && (
        <ConfirmDialog
          title="Create Product"
          message={`Are you sure you want to create the product "${pendingAction.product.name}" (ID: ${pendingAction.product.id})?`}
          confirmLabel="Create"
          onConfirm={handleCreateProductConfirm}
          onCancel={() => setPendingAction(null)}
        />
      )}

      {pendingAction?.type === 'update' && (
        <ConfirmDialog
          title="Update Product"
          message={`Are you sure you want to update "${pendingAction.product.name}"?`}
          confirmLabel="Update"
          onConfirm={handleUpdateProductConfirm}
          onCancel={() => setPendingAction(null)}
        />
      )}

      {pendingAction?.type === 'delete' && (
        <ConfirmDialog
          title="Delete Product"
          message={`Are you sure you want to delete "${pendingAction.productName}"? This action cannot be undone.`}
          confirmLabel="Delete"
          variant="danger"
          onConfirm={handleDeleteProductConfirm}
          onCancel={() => setPendingAction(null)}
        />
      )}

      {pendingAction?.type === 'inventory' && (
        <ConfirmDialog
          title="Update Inventory"
          message={`Set quantity to ${pendingAction.quantity} for "${pendingAction.productName}"?`}
          confirmLabel="Save"
          onConfirm={handleSaveInventoryConfirm}
          onCancel={() => setPendingAction(null)}
        />
      )}

      {pendingAction?.type === 'couponLimit' && (
        <ConfirmDialog
          title="Set Coupon Limit"
          message={`Set max uses to ${pendingAction.maxUses === 0 ? 'unlimited' : pendingAction.maxUses} for "${pendingAction.couponCode}"?`}
          confirmLabel="Set"
          onConfirm={async () => {
            await handleSetCouponLimitConfirm()
            if (addingCouponLimit) {
              setAddingCouponLimit(false)
              setNewCouponCode('')
              setNewCouponMaxUses('')
            }
          }}
          onCancel={() => setPendingAction(null)}
        />
      )}

      {pendingAction?.type === 'couponReset' && (
        <ConfirmDialog
          title="Reset Coupon Usage"
          message={`Reset used count to 0 for "${pendingAction.couponCode}"? This cannot be undone.`}
          confirmLabel="Reset"
          variant="danger"
          onConfirm={handleResetCouponConfirm}
          onCancel={() => setPendingAction(null)}
        />
      )}

      {toast && (
        <Toast
          type={toast.type}
          message={toast.message}
          onDismiss={() => setToast(null)}
        />
      )}
    </div>
  )
}

interface ProductFormProps {
  product: Product
  onSave: (p: Product) => void
  onCancel: () => void
}

function ProductForm({ product, onSave, onCancel }: ProductFormProps) {
  const [form, setForm] = useState({
    id: product.id,
    name: product.name,
    category: product.category,
    price: product.price,
    image: product.image ?? emptyImage,
  })

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    const img = form.image ?? emptyImage
    onSave({
      ...form,
      image: {
        thumbnail: img.thumbnail ?? '',
        mobile: img.mobile ?? img.thumbnail ?? '',
        tablet: img.tablet ?? img.thumbnail ?? '',
        desktop: img.desktop ?? img.thumbnail ?? '',
      },
    })
  }

  return (
    <form className={styles.form} onSubmit={handleSubmit}>
      <div className={styles.formRow}>
        <label>
          ID (required for new)
          <input
            type="text"
            value={form.id}
            onChange={(e) => setForm((f) => ({ ...f, id: e.target.value }))}
            required
            disabled={!!product.id}
          />
        </label>
        <label>
          Name
          <input
            type="text"
            value={form.name}
            onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
            required
          />
        </label>
      </div>
      <div className={styles.formRow}>
        <label>
          Category
          <input
            type="text"
            value={form.category}
            onChange={(e) => setForm((f) => ({ ...f, category: e.target.value }))}
          />
        </label>
        <label>
          Price
          <input
            type="number"
            step="0.01"
            min={0}
            value={form.price}
            onChange={(e) => setForm((f) => ({ ...f, price: parseFloat(e.target.value) || 0 }))}
          />
        </label>
      </div>
      <div className={styles.formRow}>
        <label>
          Image URL (thumbnail)
          <input
            type="text"
            placeholder="https://..."
            value={form.image?.thumbnail ?? ''}
            onChange={(e) =>
              setForm((f) => ({
                ...f,
                image: { ...(f.image ?? emptyImage), thumbnail: e.target.value },
              }))
            }
          />
        </label>
      </div>
      <div className={styles.formActions}>
        <button type="submit" className={styles.primaryBtn}>
          Save
        </button>
        <button type="button" onClick={onCancel} className={styles.secondaryBtn}>
          Cancel
        </button>
      </div>
    </form>
  )
}
