package transfer

type Warehouse string
type Product string
type OrderID string

type Quantity uint64
type Time uint64

type Config struct {
	TolerancePerMille uint64
	WaitDuration      uint64
}

type Line struct {
	Product  Product
	Quantity Quantity
}

type OrderStatus string

const (
	StatusCreated  OrderStatus = "created"
	StatusShipped  OrderStatus = "shipped"
	StatusClosed   OrderStatus = "closed"
	StatusCanceled OrderStatus = "canceled"
)

type orderLine struct {
	product  Product
	quantity Quantity
	shipped  Quantity
	received Quantity
	shortage Quantity
	surplus  Quantity
}

type transferOrder struct {
	id          OrderID
	source      Warehouse
	destination Warehouse
	status      OrderStatus
	lines       []orderLine
	shippedAt   Time
}

type LineView struct {
	Product  Product
	Quantity Quantity
	Shipped  Quantity
	Received Quantity
	Shortage Quantity
	Surplus  Quantity
}

type OrderView struct {
	ID          OrderID
	Source      Warehouse
	Destination Warehouse
	Status      OrderStatus
	ShippedAt   Time
	Lines       []LineView
}

type StockView struct {
	Available Quantity
	Frozen    Quantity
}
