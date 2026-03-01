import type { ProductImage as ProductImageType } from '../types'

interface ProductImageProps {
  image?: ProductImageType
  alt: string
  className?: string
}

export function ProductImage({ image, alt, className }: ProductImageProps) {
  if (!image) return null
  const src = image.desktop || image.tablet || image.mobile || image.thumbnail
  if (!src) return null
  return <img src={src} alt={alt} className={className} loading="lazy" />
}
