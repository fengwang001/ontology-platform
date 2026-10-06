package matcher

import "sync"

// NewOrderRequest describes an incoming order.
type NewOrderRequest struct {
	ID          string
	Side        Side
	Price       int64
	Quantity    int64
	Kind        OrderKind
	DisplaySize int64
}

// Result is the outcome of an accepted new-order request.
type Result struct {
	Seq    int64
	Order  Order
	Trades []Trade
}

// Stats expose internal operation counters used for complexity tests. The
// counters count structural work (display batches consumed, price levels
// stepped through, heap operations), never book sizes.
type Stats struct {
	BatchesConsumed int64
	LevelsCrossed   int64
	HeapPushes      int64
	HeapStalePops   int64
}

// Engine is a single-instrument limit order book. All methods are safe for
// concurrent use; a single mutex makes every operation appear atomic and
// gives a unique deterministic serial order.
type Engine struct {
	mu sync.RWMutex

	buys   *levelIndex
	sells  *levelIndex
	orders map[string]*bookOrder

	seq      int64
	tradeSeq int64
	trades   []Trade

	batchesConsumed int64
	levelsCrossed   int64
	heapPushes      int64
	heapStalePops   int64
}

// NewEngine creates an empty engine.
func NewEngine() *Engine {
	e := &Engine{orders: make(map[string]*bookOrder)}
	e.buys = newLevelIndex(true, &e.heapStalePops)
	e.sells = newLevelIndex(false, &e.heapStalePops)
	return e
}

func (e *Engine) nextSeq() int64 {
	e.seq++
	return e.seq
}

func (e *Engine) recordTrade(taker, maker *bookOrder, price, qty int64) Trade {
	e.tradeSeq++
	t := Trade{
		ID:        e.tradeSeq,
		TakerID:   taker.id,
		MakerID:   maker.id,
		Price:     price,
		Quantity:  qty,
		TakerSide: taker.side,
	}
	e.trades = append(e.trades, t)
	return t
}
