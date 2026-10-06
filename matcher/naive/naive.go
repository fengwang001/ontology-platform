// Package naive is an intentionally simple, independent reference
// implementation of the same matching rules used by package matcher.
//
// It deliberately uses slices and linear scans everywhere (no heaps, no
// linked lists, no aggregate counters) so that its data structures and code
// paths differ completely from the production engine. Random differential
// tests replay identical operation streams against both and require the
// resulting trade streams and order states to match exactly.
package naive

type Side int

const (
	Buy Side = iota
	Sell
)

type Kind int

const (
	Plain Kind = iota
	Iceberg
	Hidden
)

type ErrorCode int

const (
	ErrInvalid ErrorCode = iota + 1
	ErrDuplicate
	ErrNotFound
	ErrFinished
)

// Error mirrors matcher.EngineError codes.
type Error struct {
	Code ErrorCode
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

// Slot is one visible batch: the single batch of a plain order or the
// current display batch of an iceberg. Seq is batch priority; replenishment
// assigns a fresh Seq to model losing time priority.
type Slot struct {
	ID   string
	Seq  int64
	Size int64
}

type hiddenEntry struct {
	ID   string
	Seq  int64
	Size int64
}

type orderView struct {
	id          string
	seq         int64
	side        Side
	kind        Kind
	price       int64
	total       int64
	displaySize int64
	filled      int64
	cancelled   bool
}

type level struct {
	price   int64
	visible []Slot
	hidden  []hiddenEntry
	reserve map[string]int64
}

// Trade mirrors matcher.Trade.
type Trade struct {
	ID        int64
	TakerID   string
	MakerID   string
	Price     int64
	Quantity  int64
	TakerSide Side
}

// Engine is the naive reference engine.
type Engine struct {
	levels [2]map[int64]*level
	orders map[string]*orderView
	trades []Trade
	seq    int64
}

// New creates an empty reference engine.
func New() *Engine {
	return &Engine{
		levels: [2]map[int64]*level{
			Buy:  {},
			Sell: {},
		},
		orders: map[string]*orderView{},
	}
}

// Request describes an incoming order.
type Request struct {
	ID          string
	Side        Side
	Price       int64
	Quantity    int64
	Kind        Kind
	DisplaySize int64
}

func (e *Engine) nextSeq() int64 { e.seq++; return e.seq }

func (e *Engine) lvl(s Side, p int64) *level {
	m := e.levels[s]
	l := m[p]
	if l == nil {
		l = &level{price: p, reserve: map[string]int64{}}
		m[p] = l
	}
	return l
}

func (l *level) isEmpty() bool {
	return len(l.visible) == 0 && len(l.hidden) == 0 && len(l.reserve) == 0
}

func validate(r Request) *Error {
	if r.ID == "" || r.Price <= 0 || r.Quantity <= 0 {
		return &Error{ErrInvalid, "invalid parameter"}
	}
	if r.Kind == Iceberg {
		if r.DisplaySize <= 0 || r.DisplaySize > r.Quantity {
			return &Error{ErrInvalid, "invalid display size"}
		}
	} else if r.DisplaySize != 0 {
		return &Error{ErrInvalid, "display size only for iceberg"}
	}
	return nil
}

// Submit processes a new order and returns the trades it caused.
func (e *Engine) Submit(r Request) ([]Trade, *Error) {
	if verr := validate(r); verr != nil {
		return nil, verr
	}
	if _, dup := e.orders[r.ID]; dup {
		return nil, &Error{ErrDuplicate, "dup: " + r.ID}
	}
	o := &orderView{
		id: r.ID, seq: e.nextSeq(), side: r.Side, kind: r.Kind,
		price: r.Price, total: r.Quantity, displaySize: r.DisplaySize,
	}
	e.orders[o.id] = o

	trades := e.match(o)
	if o.total-o.filled > 0 {
		e.rest(o)
	}
	return trades, nil
}

func opposite(s Side) Side {
	if s == Buy {
		return Sell
	}
	return Buy
}

func crosses(s Side, takerPrice, makerPrice int64) bool {
	if s == Buy {
		return takerPrice >= makerPrice
	}
	return takerPrice <= makerPrice
}

func (e *Engine) emit(taker, makerID string, side Side, price, qty int64) Trade {
	t := Trade{
		ID: int64(len(e.trades)) + 1, TakerID: taker, MakerID: makerID,
		Price: price, Quantity: qty, TakerSide: side,
	}
	e.trades = append(e.trades, t)
	return t
}

// bestPrice returns the best live price on a side or 0/false.
func (e *Engine) bestPrice(s Side) (int64, bool) {
	var best int64
	found := false
	for p, l := range e.levels[s] {
		if l.isEmpty() {
			continue
		}
		if !found || (s == Buy && p > best) || (s == Sell && p < best) {
			best, found = p, true
		}
	}
	return best, found
}
