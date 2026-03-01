package models

// ProductImage represents the image URLs for different screen sizes
type ProductImage struct {
	Thumbnail string `json:"thumbnail"`
	Mobile   string `json:"mobile"`
	Tablet   string `json:"tablet"`
	Desktop  string `json:"desktop"`
}

// Product represents a food product
type Product struct {
	ID       string       `json:"id"`
	Name     string       `json:"name"`
	Price    float64      `json:"price"`
	Category string       `json:"category"`
	Image    ProductImage `json:"image"`
}

// OrderItemReq is a single item in an order request
type OrderItemReq struct {
	ProductID string `json:"productId"`
	Quantity  int    `json:"quantity"`
}

// OrderReq is the request body for placing an order
type OrderReq struct {
	Items      []OrderItemReq `json:"items"`
	CouponCode string         `json:"couponCode,omitempty"`
}

// OrderItem represents an item in a placed order response
type OrderItem struct {
	ProductID string `json:"productId"`
	Quantity  int    `json:"quantity"`
}

// Order is the response for a placed order
type Order struct {
	ID         string      `json:"id"`
	Total      float64     `json:"total"`
	Discounts  float64     `json:"discounts"`
	Items      []OrderItem `json:"items"`
	Products   []Product   `json:"products"`
}
