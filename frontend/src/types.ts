export interface ProductImage {
  thumbnail?: string
  mobile?: string
  tablet?: string
  desktop?: string
}

export interface Product {
  id: string
  name: string
  category: string
  price: number
  image?: ProductImage
}

export interface CartItem {
  product: Product
  quantity: number
}

export interface OrderItem {
  productId: string
  quantity: number
}

export interface OrderReq {
  items: OrderItem[]
  couponCode?: string
}

export interface Order {
  id: string
  total: number
  discounts?: number
  items: OrderItem[]
  products?: Product[]
}
