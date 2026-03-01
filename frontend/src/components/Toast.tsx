import { useEffect } from 'react'
import styles from './Toast.module.css'

interface ToastProps {
  type: 'success' | 'error'
  message: string
  onDismiss: () => void
  autoHideMs?: number
}

export function Toast({ type, message, onDismiss, autoHideMs = 4000 }: ToastProps) {
  useEffect(() => {
    if (autoHideMs <= 0) return
    const t = setTimeout(onDismiss, autoHideMs)
    return () => clearTimeout(t)
  }, [autoHideMs, onDismiss])

  return (
    <div
      className={`${styles.toast} ${type === 'success' ? styles.success : styles.error}`}
      role="alert"
      aria-live="polite"
    >
      <span className={styles.message}>{message}</span>
      <button
        type="button"
        className={styles.closeBtn}
        onClick={onDismiss}
        aria-label="Dismiss"
      >
        ×
      </button>
    </div>
  )
}
