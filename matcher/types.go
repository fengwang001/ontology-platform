// Package matcher implements a single-instrument limit-order matching engine
// that supports plain (visible) orders, iceberg orders, and hidden orders.
package matcher

// Side selects whether an order buys or sells the instrument.
type Side int

const (
	Buy Side = iota
	Sell
)

// OrderKind enumerates the supported order types.
type OrderKind int

const (
	// Plain orders are fully visible in the book at all times.
	Plain OrderKind = iota
	// Iceberg orders show only a small display batch at a time; once a
	// displayed batch is fully filled a fresh batch is appended at the tail
	// of the price level's visible queue.
	Iceberg
	// Hidden orders are never included in visible-quantity statistics and
	// only trade after every visible batch and every iceberg reserve at the
	// price level has been exhausted.
	Hidden
)

// OrderStatus is the lifecycle state of an order.
type OrderStatus int

const (
	// Resting means the order rests in the book and nothing has filled yet.
	Resting OrderStatus = iota
	// PartiallyFilled means some quantity has filled and some still rests.
	PartiallyFilled
	// Filled means the whole current total quantity has traded.
	Filled
	// Cancelled means the order was cancelled before being fully filled.
	Cancelled
)

func (s OrderStatus) String() string {
	switch s {
	case Resting:
		return "resting"
	case PartiallyFilled:
		return "partially_filled"
	case Filled:
		return "filled"
	case Cancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

// Order is an immutable view of an order at a point in time, returned by the
// query methods. It never aliases mutable engine state.
type Order struct {
	ID string
	// Seq is the monotonically increasing priority number assigned by the
	// engine when the order was accepted. Increase-quantity modifications
	// receive a fresh Seq and therefore lose time priority.
	Seq   int64
	Side  Side
	Kind  OrderKind
	Price int64
	// Total is the current total quantity (a decrease/modify can shrink it).
	Total int64
	// DisplaySize is the configured display batch size; meaningful for iceberg.
	DisplaySize int64
	// Filled is the cumulative traded quantity.
	Filled int64
	// Remaining is Total - Filled.
	Remaining int64
	// Visible is the currently displayed batch size resting in the book.
	// For hidden orders and for a not-yet-replenished iceberg it is zero.
	Visible int64
	Status  OrderStatus
}

// Trade is one execution between an aggressive (incoming) order and a
// resting (passive) order. Price is always the passive order's price.
type Trade struct {
	ID        int64
	TakerID   string
	MakerID   string
	Price     int64
	Quantity  int64
	TakerSide Side
}

// PriceLevelView exposes the visible quantity at one price. Hidden quantity
// and iceberg reserve are deliberately absent.
type PriceLevelView struct {
	Price      int64
	VisibleQty int64
}
