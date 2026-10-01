// Package orderbook implements a price-time priority limit order book
// matching engine.
//
// Matching rules:
//   - A buy order matches resting sells whose price is <= the buy price
//     (symmetrically for sells against bids).
//   - The aggressor takes the best opposite price first; within a price
//     level, orders are consumed in arrival order (smallest sequence first).
//   - Every trade executes at the passive (resting) order's price, for
//     min(aggressor remaining, passive remaining).
//   - Any leftover of the aggressor rests at its own price, at the tail
//     of that price level.
//
// Amend semantics: shrinking the remaining quantity keeps the queue
// position; growing it moves the order to the tail of its price level
// with a fresh arrival sequence; amending to the current remaining is a
// no-op that still succeeds.
//
// All methods are safe for concurrent use; results are equivalent to
// some serial execution order.
package orderbook

import (
	"container/list"
	"errors"
	"sync"
)

// MaxLimit is the inclusive upper bound for price and quantity (1e12).
const MaxLimit = int64(1_000_000_000_000)

// Rejection reasons. Every rejected operation leaves the book, the trade
// stream and the arrival sequence untouched.
var (
	ErrInvalidTick    = errors.New("orderbook: tick must be a positive int64")
	ErrDuplicateID    = errors.New("orderbook: order id already used")
	ErrInvalidSide    = errors.New("orderbook: side must be Buy or Sell")
	ErrInvalidPrice   = errors.New("orderbook: price must be positive, <= 1e12 and a multiple of tick")
	ErrInvalidQty     = errors.New("orderbook: qty must be positive and <= 1e12")
	ErrUnknownID      = errors.New("orderbook: order id never seen")
	ErrOrderFilled    = errors.New("orderbook: order already fully filled")
	ErrOrderCancelled = errors.New("orderbook: order already cancelled")
	ErrInvalidNewQty  = errors.New("orderbook: new qty must be positive and <= 1e12")
)

// Side is the direction of an order.
type Side int

const (
	Buy Side = iota
	Sell
)

func (s Side) String() string {
	switch s {
	case Buy:
		return "buy"
	case Sell:
		return "sell"
	default:
		return "invalid"
	}
}

// Trade is a single execution, reported in the order it occurred.
type Trade struct {
	PassiveID    string // resting (maker) order id
	AggressiveID string // incoming (taker) order id
	Price        int64  // always the passive order's price
	Qty          int64
}

// DepthLevel is one aggregated price level returned by Depth.
type DepthLevel struct {
	Price      int64
	TotalQty   int64
	OrderCount int
}

type orderState int

const (
	stateOpen orderState = iota
	stateFilled
	stateCancelled
)

type order struct {
	id        string
	side      Side
	price     int64
	remaining int64
	seq       int64
	elem      *list.Element // element in its level's queue
}

type level struct {
	price int64
	queue *list.List // FIFO of *order, arrival order
	total int64      // sum of remaining over the queue
}

// sideBook holds one side of the book. prices is kept sorted: ascending
// for asks (best = lowest first), descending for bids (best = highest
// first). Levels with zero total are removed eagerly.
type sideBook struct {
	desc   bool
	prices []int64
	levels map[int64]*level
}

func newSideBook(desc bool) *sideBook {
	return &sideBook{desc: desc, levels: make(map[int64]*level)}
}

// locate returns the index of price in prices, or the insertion point.
func (s *sideBook) locate(price int64) (int, bool) {
	lo, hi := 0, len(s.prices)
	for lo < hi {
		mid := (lo + hi) / 2
		p := s.prices[mid]
		switch {
		case p == price:
			return mid, true
		case s.desc == (p > price):
			lo = mid + 1
		default:
			hi = mid
		}
	}
	return lo, false
}

func (s *sideBook) best() *level {
	if len(s.prices) == 0 {
		return nil
	}
	return s.levels[s.prices[0]]
}

func (s *sideBook) getOrCreate(price int64) *level {
	if lv, ok := s.levels[price]; ok {
		return lv
	}
	idx, _ := s.locate(price)
	s.prices = append(s.prices, 0)
	copy(s.prices[idx+1:], s.prices[idx:])
	s.prices[idx] = price
	lv := &level{price: price, queue: list.New()}
	s.levels[price] = lv
	return lv
}

func (s *sideBook) removeIfEmpty(lv *level) {
	if lv.queue.Len() != 0 {
		return
	}
	idx, ok := s.locate(lv.price)
	if !ok {
		panic("orderbook: level missing from sorted prices")
	}
	s.prices = append(s.prices[:idx], s.prices[idx+1:]...)
	delete(s.levels, lv.price)
}

// OrderBook is a price-time priority limit order book.
type OrderBook struct {
	mu     sync.Mutex
	tick   int64
	seq    int64 // arrival sequence; only advances on accepted events
	bids   *sideBook
	asks   *sideBook
	live   map[string]*order     // orders currently resting in the book
	status map[string]orderState // every id ever accepted
}

// NewOrderBook creates an empty book with the given minimum price
// increment. tick must be positive.
func NewOrderBook(tick int64) (*OrderBook, error) {
	if tick <= 0 {
		return nil, ErrInvalidTick
	}
	return &OrderBook{
		tick:   tick,
		bids:   newSideBook(true),
		asks:   newSideBook(false),
		live:   make(map[string]*order),
		status: make(map[string]orderState),
	}, nil
}

// Submit accepts a limit order, matches it against the opposite side and
// rests any leftover. It returns the trades in the order they occurred
// (nil if none). Rejections are checked in this order, first match wins:
// duplicate id, invalid side, invalid price, invalid qty.
func (b *OrderBook) Submit(id string, side Side, price, qty int64) ([]Trade, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, used := b.status[id]; used {
		return nil, ErrDuplicateID
	}
	if side != Buy && side != Sell {
		return nil, ErrInvalidSide
	}
	if price <= 0 || price > MaxLimit || price%b.tick != 0 {
		return nil, ErrInvalidPrice
	}
	if qty <= 0 || qty > MaxLimit {
		return nil, ErrInvalidQty
	}

	var incoming, opposite *sideBook
	if side == Buy {
		incoming, opposite = b.bids, b.asks
	} else {
		incoming, opposite = b.asks, b.bids
	}

	var trades []Trade
	remaining := qty
	for remaining > 0 {
		lv := opposite.best()
		if lv == nil || !crosses(side, price, lv.price) {
			break
		}
		front := lv.queue.Front().Value.(*order)
		tq := min(remaining, front.remaining)
		trades = append(trades, Trade{
			PassiveID:    front.id,
			AggressiveID: id,
			Price:        front.price,
			Qty:          tq,
		})
		front.remaining -= tq
		lv.total -= tq
		remaining -= tq
		if front.remaining == 0 {
			lv.queue.Remove(front.elem)
			delete(b.live, front.id)
			b.status[front.id] = stateFilled
		}
		opposite.removeIfEmpty(lv)
	}

	if remaining > 0 {
		b.seq++
		o := &order{id: id, side: side, price: price, remaining: remaining, seq: b.seq}
		lv := incoming.getOrCreate(price)
		o.elem = lv.queue.PushBack(o)
		lv.total += remaining
		b.live[id] = o
		b.status[id] = stateOpen
	} else {
		b.status[id] = stateFilled
	}
	return trades, nil
}

// crosses reports whether a resting order at restPrice can match an
// incoming order at price on the given side.
func crosses(side Side, price, restPrice int64) bool {
	if side == Buy {
		return restPrice <= price
	}
	return restPrice >= price
}

// lookupLive resolves the three distinguishable id failures shared by
// Cancel and Amend.
func (b *OrderBook) lookupLive(id string) (*order, error) {
	st, seen := b.status[id]
	if !seen {
		return nil, ErrUnknownID
	}
	switch st {
	case stateFilled:
		return nil, ErrOrderFilled
	case stateCancelled:
		return nil, ErrOrderCancelled
	}
	return b.live[id], nil
}

// Cancel removes a resting order and returns its remaining quantity.
func (b *OrderBook) Cancel(id string) (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	o, err := b.lookupLive(id)
	if err != nil {
		return 0, err
	}
	sb := b.bids
	if o.side == Sell {
		sb = b.asks
	}
	lv := sb.levels[o.price]
	lv.queue.Remove(o.elem)
	lv.total -= o.remaining
	sb.removeIfEmpty(lv)
	delete(b.live, id)
	b.status[id] = stateCancelled
	return o.remaining, nil
}

// Amend changes the remaining quantity of a resting order to newQty.
// Shrinking keeps the queue position; growing moves the order to the
// tail of its price level with a new arrival sequence; an equal newQty
// is a successful no-op.
func (b *OrderBook) Amend(id string, newQty int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	o, err := b.lookupLive(id)
	if err != nil {
		return err
	}
	if newQty <= 0 || newQty > MaxLimit {
		return ErrInvalidNewQty
	}
	cur := o.remaining
	if newQty == cur {
		return nil
	}
	sb := b.bids
	if o.side == Sell {
		sb = b.asks
	}
	lv := sb.levels[o.price]
	if newQty < cur {
		lv.total -= cur - newQty
		o.remaining = newQty
		return nil
	}
	// Grow: re-arrive at the tail of the same price level.
	lv.queue.Remove(o.elem)
	b.seq++
	o.seq = b.seq
	o.elem = lv.queue.PushBack(o)
	lv.total += newQty - cur
	o.remaining = newQty
	return nil
}

// Depth returns up to n best price levels on the given side: highest
// price first for Buy, lowest first for Sell. Only levels with positive
// remaining quantity are included.
func (b *OrderBook) Depth(side Side, n int) []DepthLevel {
	b.mu.Lock()
	defer b.mu.Unlock()

	if n <= 0 {
		return nil
	}
	sb := b.bids
	if side == Sell {
		sb = b.asks
	}
	out := make([]DepthLevel, 0, min(n, len(sb.prices)))
	for _, p := range sb.prices {
		if len(out) >= n {
			break
		}
		lv := sb.levels[p]
		if lv.total <= 0 {
			continue
		}
		out = append(out, DepthLevel{
			Price:      p,
			TotalQty:   lv.total,
			OrderCount: lv.queue.Len(),
		})
	}
	return out
}
