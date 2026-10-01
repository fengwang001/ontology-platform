// Package orderbook implements a price-time priority limit order book
// matching engine.
package orderbook

import (
	"container/list"
	"errors"
	"sync"
)

// Side is the direction of an order.
type Side int

const (
	// Buy is the bid side.
	Buy Side = 1
	// Sell is the ask side.
	Sell Side = -1
)

func (s Side) valid() bool { return s == Buy || s == Sell }

// MaxValue is the inclusive upper bound for price, quantity and tick.
const MaxValue int64 = 1_000_000_000_000

// Order is an incoming limit order.
type Order struct {
	ID    int64
	Side  Side
	Price int64
	Qty   int64
}

// Trade is one fill. PassiveID is the resting order, AggressorID the
// incoming order; Price is the passive order's price.
type Trade struct {
	PassiveID   int64
	AggressorID int64
	Price       int64
	Qty         int64
}

// Level is one aggregated price level in a depth query.
type Level struct {
	Price int64
	Qty   int64
	Count int64
}

// Rejected-operation reasons. Sentinel errors are returned directly; use
// errors.Is to distinguish them.
var (
	// ErrTickNonPositive: NewEngine received a non-positive tick.
	ErrTickNonPositive = errors.New("tick must be positive")
	// ErrIDReused: an order id was used before (filled, cancelled or live).
	ErrIDReused = errors.New("order id already used")
	// ErrInvalidSide: side is neither Buy nor Sell.
	ErrInvalidSide = errors.New("invalid side")
	// ErrInvalidPrice: price non-positive, over 1e12, or not a multiple of tick.
	ErrInvalidPrice = errors.New("invalid price")
	// ErrInvalidQty: quantity non-positive or over 1e12.
	ErrInvalidQty = errors.New("invalid quantity")
	// ErrUnknownID: Cancel/Amend on an id that never appeared.
	ErrUnknownID = errors.New("unknown order id")
	// ErrAlreadyFilled: the order fully traded and left the book.
	ErrAlreadyFilled = errors.New("order already fully filled")
	// ErrAlreadyCancelled: the order was cancelled and left the book.
	ErrAlreadyCancelled = errors.New("order already cancelled")
)

// Engine is a concurrency-safe price-time priority limit order book.
// The zero value is not usable; use NewEngine.
type Engine struct {
	mu     sync.Mutex
	tick   int64
	seq    int64
	orders map[int64]*order
	bids   *bookSide
	asks   *bookSide
}

type orderState int

const (
	stateLive orderState = iota
	stateFilled
	stateCancelled
)

type order struct {
	id        int64
	side      Side
	price     int64
	remaining int64
	seq       int64
	state     orderState
	elem      *list.Element
}

type bookSide struct {
	side   Side
	levels *list.List // *level, ordered best-first
}

type level struct {
	price  int64
	qty    int64
	count  int64
	orders *list.List // *order at this price, arrival order
}

// NewEngine creates an engine with the given minimum quote increment.
func NewEngine(tick int64) (*Engine, error) {
	if tick <= 0 {
		return nil, ErrTickNonPositive
	}
	return &Engine{
		tick:   tick,
		orders: make(map[int64]*order),
		bids:   &bookSide{side: Buy, levels: list.New()},
		asks:   &bookSide{side: Sell, levels: list.New()},
	}, nil
}

// Submit applies a limit order: it first trades against the opposite book,
// then any remainder rests at its price. Rejected operations change nothing.
func (e *Engine) Submit(o Order) ([]Trade, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Validation order: id reuse, side, price, quantity. Nothing is
	// mutated before every check passes.
	if _, ok := e.orders[o.ID]; ok {
		return nil, ErrIDReused
	}
	if !o.Side.valid() {
		return nil, ErrInvalidSide
	}
	if o.Price <= 0 || o.Price > MaxValue || o.Price%e.tick != 0 {
		return nil, ErrInvalidPrice
	}
	if o.Qty <= 0 || o.Qty > MaxValue {
		return nil, ErrInvalidQty
	}

	aggr := &order{
		id:        o.ID,
		side:      o.Side,
		price:     o.Price,
		remaining: o.Qty,
		state:     stateFilled,
	}

	own, opp := e.bids, e.asks
	if o.Side == Sell {
		own, opp = e.asks, e.bids
	}

	trades := make([]Trade, 0)
	for aggr.remaining > 0 {
		bestElem := opp.levels.Front()
		if bestElem == nil {
			break
		}
		best := bestElem.Value.(*level)
		if !crosses(aggr.price, best.price, o.Side) {
			break
		}
		for aggr.remaining > 0 {
			headElem := best.orders.Front()
			if headElem == nil {
				break
			}
			passive := headElem.Value.(*order)
			fill := aggr.remaining
			if passive.remaining < fill {
				fill = passive.remaining
			}
			trades = append(trades, Trade{
				PassiveID:   passive.id,
				AggressorID: aggr.id,
				Price:       passive.price,
				Qty:         fill,
			})
			aggr.remaining -= fill
			passive.remaining -= fill
			best.qty -= fill
			if passive.remaining == 0 {
				passive.state = stateFilled
				best.orders.Remove(headElem)
				best.count--
			}
			if aggr.remaining == 0 {
				break
			}
		}
		if best.orders.Len() == 0 {
			opp.levels.Remove(bestElem)
		}
	}

	if aggr.remaining > 0 {
		e.seq++
		aggr.seq = e.seq
		aggr.state = stateLive
		lvl := e.levelFor(own, aggr.price)
		aggr.elem = lvl.orders.PushBack(aggr)
		lvl.qty += aggr.remaining
		lvl.count++
	}
	e.orders[aggr.id] = aggr
	_ = o
	return trades, nil
}

// Cancel removes a live order and returns its cancelled remaining quantity.
func (e *Engine) Cancel(id int64) (int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	ord, ok := e.orders[id]
	if !ok {
		return 0, ErrUnknownID
	}
	switch ord.state {
	case stateFilled:
		return 0, ErrAlreadyFilled
	case stateCancelled:
		return 0, ErrAlreadyCancelled
	}

	removed := ord.remaining
	own := e.sideOf(ord.side)
	lvlElem := own.indexElem(ord.price)
	lvl := lvlElem.Value.(*level)
	lvl.orders.Remove(ord.elem)
	lvl.qty -= removed
	lvl.count--
	ord.remaining = 0
	ord.state = stateCancelled
	if lvl.orders.Len() == 0 {
		own.levels.Remove(lvlElem)
	}
	return removed, nil
}

// Amend changes a live order's remaining quantity. A decrease keeps queue
// position; an increase moves the order to the price-level tail with a new
// arrival sequence; equal quantity is a successful no-op.
func (e *Engine) Amend(id int64, newQty int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	ord, ok := e.orders[id]
	if !ok {
		return ErrUnknownID
	}
	switch ord.state {
	case stateFilled:
		return ErrAlreadyFilled
	case stateCancelled:
		return ErrAlreadyCancelled
	}
	if newQty <= 0 || newQty > MaxValue {
		return ErrInvalidQty
	}

	old := ord.remaining
	switch {
	case newQty == old:
		return nil
	}

	own := e.sideOf(ord.side)
	lvlElem := own.indexElem(ord.price)
	lvl := lvlElem.Value.(*level)
	if newQty > old {
		e.seq++
		ord.seq = e.seq
		lvl.orders.MoveToBack(ord.elem)
	}
	lvl.qty += newQty - old
	ord.remaining = newQty
	return nil
}

// Depth returns the best n price levels of the given side.
func (e *Engine) Depth(side Side, n int) ([]Level, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !side.valid() {
		return nil, ErrInvalidSide
	}
	if n <= 0 {
		return []Level{}, nil
	}
	bs := e.sideOf(side)
	out := make([]Level, 0, min(n, bs.levels.Len()))
	for elem := bs.levels.Front(); elem != nil && len(out) < n; elem = elem.Next() {
		lvl := elem.Value.(*level)
		if lvl.qty > 0 {
			out = append(out, Level{Price: lvl.price, Qty: lvl.qty, Count: lvl.count})
		}
	}
	return out, nil
}

func crosses(aggrPrice, passivePrice int64, aggrSide Side) bool {
	if aggrSide == Buy {
		return passivePrice <= aggrPrice
	}
	return passivePrice >= aggrPrice
}

func (e *Engine) sideOf(s Side) *bookSide {
	if s == Buy {
		return e.bids
	}
	return e.asks
}

func (bs *bookSide) indexElem(price int64) *list.Element {
	for elem := bs.levels.Front(); elem != nil; elem = elem.Next() {
		lvl := elem.Value.(*level)
		if lvl.price == price {
			return elem
		}
	}
	return nil
}

// levelFor returns the level at price, creating it in best-first order.
// Bids are sorted price descending; asks price ascending.
func (e *Engine) levelFor(bs *bookSide, price int64) *level {
	for elem := bs.levels.Front(); elem != nil; elem = elem.Next() {
		lvl := elem.Value.(*level)
		if lvl.price == price {
			return lvl
		}
		better := lvl.price < price
		if bs.side == Sell {
			better = lvl.price > price
		}
		if better {
			newLevel := &level{price: price, orders: list.New()}
			bs.levels.InsertBefore(newLevel, elem)
			return newLevel
		}
	}
	newLevel := &level{price: price, orders: list.New()}
	bs.levels.PushBack(newLevel)
	return newLevel
}
