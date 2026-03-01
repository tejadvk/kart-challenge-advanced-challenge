import styles from './EmptyCart.module.css'

export function EmptyCart() {
  return (
    <div className={styles.wrapper}>
      <div className={styles.illustration} aria-hidden>
        <svg viewBox="0 0 80 80" fill="none" xmlns="http://www.w3.org/2000/svg">
          <circle cx="35" cy="45" r="20" fill="#e8e4e0" />
          <path d="M35 35v20M25 45h20" stroke="#c4bfb8" strokeWidth="2" strokeLinecap="round" />
          <circle cx="55" cy="35" r="18" fill="#f5d5d0" />
          <path d="M55 27v16M46 35h18" stroke="#e8b5ad" strokeWidth="1.5" strokeLinecap="round" />
        </svg>
      </div>
      <p className={styles.text}>Your added items will appear here</p>
    </div>
  )
}
