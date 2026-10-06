// Package naive 是对照撮合引擎的独立朴素参考模型。
//
// 它刻意用最直白的切片与线性扫描重新实现规格文字，不引用 matching 包的
// 任何内部结构，以便在随机差分测试中充当“独立第二实现”。
package naive

import "fmt"

type Side int

const (
	Buy Side = iota + 1
	Sell
)

type OrderType int

const (
	Limit OrderType = iota + 1
	Iceberg
	Hidden
)

type Status int

const (
	Pending Status = iota + 1
	Partial
	Completed
	Cancelled
)

type Params struct {
	ClientID          int64
	Side              Side
	Price             int64
	TotalQty          int64
	Type              OrderType
	IcebergVisibleQty int64
}

type Order struct {
	ClientID     int64
	Seq          int64
	Side         Side
	Price        int64
	Type         OrderType
	TotalQty     int64
	FilledQty    int64
	RemainingQty int64
	VisibleQty   int64
	Status       Status
}

type Fill struct {
	Seq          int64
	BuyClientID  int64
	SellClientID int64
	Price        int64
	Qty          int64
	Aggressor    Side
	AggClientID  int64
}

type Depth struct {
	Price      int64
	VisibleQty int64
}

// visItem 是价位显示队列上的一个条目（一个显示批）。
type visItem struct {
	id   int64
	left int64
}

type orderRec struct {
	o       Order
	showPar int64 // 冰山显示量参数
}

type Model struct {
	nextSeq int64
	orders  map[int64]*orderRec
	vis     map[Side]map[int64][]visItem
	hidden  map[Side]map[int64][]int64
	fills   []Fill
}

func New() *Model {
	return &Model{
		orders: map[int64]*orderRec{},
		vis: map[Side]map[int64][]visItem{
			Buy:  {},
			Sell: {},
		},
		hidden: map[Side]map[int64][]int64{
			Buy:  {},
			Sell: {},
		},
	}
}

type ErrorKind int

const (
	ErrInvalidParam ErrorKind = iota
	ErrDuplicateClientID
	ErrOrderNotFound
	ErrOrderCompleted
	ErrOrderCancelled
)

type Error struct {
	Kind ErrorKind
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func mkErr(k ErrorKind, f string, a ...any) error {
	return &Error{Kind: k, Msg: fmt.Sprintf(f, a...)}
}

func validate(p Params) error {
	if p.ClientID <= 0 || p.Price <= 0 || p.TotalQty <= 0 {
		return mkErr(ErrInvalidParam, "non-positive field: client=%d price=%d qty=%d", p.ClientID, p.Price, p.TotalQty)
	}
	if p.Side != Buy && p.Side != Sell {
		return mkErr(ErrInvalidParam, "bad side %d", p.Side)
	}
	switch p.Type {
	case Limit, Hidden:
	case Iceberg:
		if p.IcebergVisibleQty <= 0 || p.IcebergVisibleQty > p.TotalQty {
			return mkErr(ErrInvalidParam, "bad iceberg show %d/%d", p.IcebergVisibleQty, p.TotalQty)
		}
	default:
		return mkErr(ErrInvalidParam, "bad type %d", p.Type)
	}
	return nil
}

func (m *Model) addFill(buy, sell int64, price, qty int64, aggr Side, aggrID int64) {
	m.fills = append(m.fills, Fill{
		Seq:          int64(len(m.fills) + 1),
		BuyClientID:  buy,
		SellClientID: sell,
		Price:        price,
		Qty:          qty,
		Aggressor:    aggr,
		AggClientID:  aggrID,
	})
}

func (m *Model) fillParties(aggr, rest *orderRec, price, qty int64) {
	for _, p := range [2]*orderRec{aggr, rest} {
		p.o.FilledQty += qty
		p.o.RemainingQty -= qty
		if p.o.RemainingQty == 0 {
			p.o.Status = Completed
		} else {
			p.o.Status = Partial
		}
	}
	if aggr.o.Side == Buy {
		m.addFill(aggr.o.ClientID, rest.o.ClientID, price, qty, Buy, aggr.o.ClientID)
	} else {
		m.addFill(rest.o.ClientID, aggr.o.ClientID, price, qty, Sell, aggr.o.ClientID)
	}
}

// rest 把委托挂入簿内。
func (m *Model) rest(o *orderRec) {
	if o.o.Type == Hidden {
		m.hidden[o.o.Side][o.o.Price] = append(m.hidden[o.o.Side][o.o.Price], o.o.ClientID)
		return
	}
	size := o.o.RemainingQty
	if o.o.Type == Iceberg && o.showPar < size {
		size = o.showPar
	}
	m.vis[o.o.Side][o.o.Price] = append(m.vis[o.o.Side][o.o.Price], visItem{id: o.o.ClientID, left: size})
}

// Submit 接收新委托并立即与对手盘撮合。返回被接受序号与本次成交。
func (m *Model) Submit(p Params) (int64, []Fill, error) {
	if err := validate(p); err != nil {
		return 0, nil, err
	}
	if _, ok := m.orders[p.ClientID]; ok {
		return 0, nil, mkErr(ErrDuplicateClientID, "dup %d", p.ClientID)
	}
	start := len(m.fills)
	m.nextSeq++
	rec := &orderRec{
		o: Order{
			ClientID:     p.ClientID,
			Seq:          m.nextSeq,
			Side:         p.Side,
			Price:        p.Price,
			Type:         p.Type,
			TotalQty:     p.TotalQty,
			RemainingQty: p.TotalQty,
			Status:       Pending,
		},
		showPar: p.IcebergVisibleQty,
	}
	if p.Type == Limit {
		rec.o.VisibleQty = p.TotalQty
	} else {
		rec.o.VisibleQty = p.IcebergVisibleQty
	}
	m.orders[p.ClientID] = rec

	opp := Sell
	if p.Side == Sell {
		opp = Buy
	}

	for rec.o.RemainingQty > 0 {
		best, found := m.bestCrossPrice(opp, p.Side, p.Price)
		if !found {
			break
		}

		// 第一阶段：显示队列，逐批成交，一次只消耗一个批；
		// 冰山批吃完立即补批排到队尾，可在本次吃单中继续被吃到。
		for rec.o.RemainingQty > 0 && len(m.vis[opp][best]) > 0 {
			item := m.vis[opp][best][0]
			rest := m.orders[item.id]
			q := item.left
			if rec.o.RemainingQty < q {
				q = rec.o.RemainingQty
			}
			m.fillParties(rec, rest, best, q)
			item.left -= q
			if item.left == 0 {
				m.vis[opp][best] = m.vis[opp][best][1:]
				if rest.o.Type == Iceberg && rest.o.Status != Completed {
					size := rest.o.RemainingQty
					if rest.showPar < size {
						size = rest.showPar
					}
					m.vis[opp][best] = append(m.vis[opp][best], visItem{id: rest.o.ClientID, left: size})
				}
			} else {
				m.vis[opp][best][0] = item
			}
		}

		// 第二阶段：显示队列空（含全部冰山储备耗尽）才吃隐藏委托。
		for rec.o.RemainingQty > 0 && len(m.hidden[opp][best]) > 0 {
			hid := m.hidden[opp][best][0]
			rest := m.orders[hid]
			q := rest.o.RemainingQty
			if rec.o.RemainingQty < q {
				q = rec.o.RemainingQty
			}
			m.fillParties(rec, rest, best, q)
			if rest.o.Status == Completed {
				m.hidden[opp][best] = m.hidden[opp][best][1:]
			}
		}

		if rec.o.RemainingQty == 0 {
			break
		}
		if len(m.vis[opp][best]) == 0 && len(m.hidden[opp][best]) == 0 {
			delete(m.vis[opp], best)
			delete(m.hidden[opp], best)
		}
	}

	if rec.o.RemainingQty > 0 {
		m.rest(rec)
	}
	return rec.o.Seq, append([]Fill(nil), m.fills[start:]...), nil
}

// bestCrossPrice 线性扫描对手盘，返回最优交叉价位（卖盘最低/买盘最高）。
func (m *Model) bestCrossPrice(opp, aggrSide Side, aggrPrice int64) (int64, bool) {
	best := int64(0)
	found := false
	candidate := func(price int64) {
		cross := aggrSide == Buy && aggrPrice >= price || aggrSide == Sell && aggrPrice <= price
		if !cross {
			return
		}
		if !found {
			best, found = price, true
			return
		}
		if opp == Sell && price < best || opp == Buy && price > best {
			best = price
		}
	}
	for price, q := range m.vis[opp] {
		if len(q) > 0 {
			candidate(price)
		}
	}
	for price, h := range m.hidden[opp] {
		if len(h) > 0 {
			candidate(price)
		}
	}
	return best, found
}

func (m *Model) pull(rec *orderRec) {
	s, p := rec.o.Side, rec.o.Price
	if rec.o.Type == Hidden {
		q := m.hidden[s][p]
		for i, id := range q {
			if id == rec.o.ClientID {
				m.hidden[s][p] = append(q[:i], q[i+1:]...)
				break
			}
		}
		return
	}
	q := m.vis[s][p]
	out := q[:0]
	for _, it := range q {
		if it.id != rec.o.ClientID {
			out = append(out, it)
		}
	}
	m.vis[s][p] = out
}

func (m *Model) Cancel(id int64) error {
	rec, ok := m.orders[id]
	if !ok {
		return mkErr(ErrOrderNotFound, "not found %d", id)
	}
	switch rec.o.Status {
	case Completed:
		return mkErr(ErrOrderCompleted, "completed %d", id)
	case Cancelled:
		return mkErr(ErrOrderCancelled, "cancelled %d", id)
	}
	m.pull(rec)
	rec.o.Status = Cancelled
	return nil
}

func (m *Model) ReplaceQty(id, newRemaining int64) error {
	if newRemaining <= 0 {
		return mkErr(ErrInvalidParam, "non-positive %d", newRemaining)
	}
	rec, ok := m.orders[id]
	if !ok {
		return mkErr(ErrOrderNotFound, "not found %d", id)
	}
	switch rec.o.Status {
	case Completed:
		return mkErr(ErrOrderCompleted, "completed %d", id)
	case Cancelled:
		return mkErr(ErrOrderCancelled, "cancelled %d", id)
	}
	old := rec.o.RemainingQty
	rec.o.TotalQty = rec.o.FilledQty + newRemaining
	rec.o.RemainingQty = newRemaining

	if newRemaining > old {
		// 加量：摘出、取新序号、排到队尾；冰山以满批重新开始。
		m.pull(rec)
		m.nextSeq++
		rec.o.Seq = m.nextSeq
		m.rest(rec)
	} else if rec.o.Type != Hidden {
		// 减量保位；当前显示批同步截短。
		q := m.vis[rec.o.Side][rec.o.Price]
		for i := range q {
			if q[i].id == id && q[i].left > newRemaining {
				q[i].left = newRemaining
			}
		}
	}
	return nil
}

func (m *Model) GetOrder(id int64) (Order, bool) {
	rec, ok := m.orders[id]
	if !ok {
		return Order{}, false
	}
	return rec.o, true
}

func (m *Model) Fills() []Fill { return append([]Fill(nil), m.fills...) }

func (m *Model) VisibleDepthAt(s Side, price int64) int64 {
	var sum int64
	for _, it := range m.vis[s][price] {
		sum += it.left
	}
	return sum
}

func bestPrice(qs map[int64][]visItem, wantMin bool) (int64, bool) {
	best, found := int64(0), false
	for p, q := range qs {
		if len(q) == 0 {
			continue
		}
		if !found || (wantMin && p < best) || (!wantMin && p > best) {
			best, found = p, true
		}
	}
	return best, found
}

// BestBid/BestAsk 反映簿内真实可成交流动性：隐藏委托虽然不计入显示量，
// 但仍占据价位并决定最优价（否则会出现“查询无盘口却能成交”的矛盾）。
func (m *Model) bestPriceInclHidden(s Side, wantMin bool) (int64, bool) {
	best, found := int64(0), false
	consider := func(p int64) {
		if !found || (wantMin && p < best) || (!wantMin && p > best) {
			best, found = p, true
		}
	}
	for p, q := range m.vis[s] {
		if len(q) > 0 {
			consider(p)
		}
	}
	for p, h := range m.hidden[s] {
		if len(h) > 0 {
			consider(p)
		}
	}
	return best, found
}

func (m *Model) BestBid() (int64, bool) { return m.bestPriceInclHidden(Buy, false) }
func (m *Model) BestAsk() (int64, bool) { return m.bestPriceInclHidden(Sell, true) }

func (m *Model) SnapshotDepth(s Side) []Depth {
	var out []Depth
	for p, q := range m.vis[s] {
		var sum int64
		for _, it := range q {
			sum += it.left
		}
		if sum > 0 {
			out = append(out, Depth{Price: p, VisibleQty: sum})
		}
	}
	return out
}
