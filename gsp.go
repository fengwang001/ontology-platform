package ontology

import (
	"sort"
	"sync"
)

// ErrorKind 标识被拒绝操作的原因，按规则规定的优先级只报第一个。
type ErrorKind int

const (
	ErrInvalidArgument ErrorKind = iota + 1
	ErrBidderExists
	ErrAuctionNotFound
	ErrAuctionResolved
	ErrClickNotWinner
	ErrNoParticipants
)

// OpError 携带可区分的拒绝原因。
type OpError struct {
	Kind ErrorKind
}

func (e *OpError) Error() string {
	switch e.Kind {
	case ErrInvalidArgument:
		return "invalid argument"
	case ErrBidderExists:
		return "bidder already exists"
	case ErrAuctionNotFound:
		return "auction not found"
	case ErrAuctionResolved:
		return "auction already resolved"
	case ErrClickNotWinner:
		return "click id is not a winner"
	case ErrNoParticipants:
		return "no eligible participants"
	default:
		return "unknown error"
	}
}

// Winner 是拍卖按名次返回的赢家条目。
type Winner struct {
	ID  string
	P   int64
	Qe  int64 // Auction 时刻冻结的有效质量分
	Seq int64 // 登记序号
}

// Bidder 是内部登记信息（未导出字段通过方法/查询读取）。
type bidder struct {
	id      string
	bid     int64
	q       int64
	budget  int64
	initial int64
	h       int64 // 未结算占用额
	f       int64 // 连续未点击中标数
	seq     int64
}

// winRec 记录一次中标的计价与冻结的有效质量分。
type winRec struct {
	bidderSeq int64
	id        string
	p         int64
	qe        int64
	clicked   bool
}

// auctionRec 记录一次拍卖。
type auctionRec struct {
	id       int64
	winners  []*winRec
	resolved bool
}

// Engine 是可并发调用的 GSP 竞价结算器。
type Engine struct {
	mu       sync.RWMutex
	bidders  map[string]*bidder
	bySeq    map[int64]*bidder
	auctions map[int64]*auctionRec
	nextSeq  int64
	nextAuc  int64

	// sortCompares 为非导出计数器：Auction 排序期间的比较次数，
	// 用于验证“只对参拍者排一次序”。
	sortCompares int64
}

// NewEngine 创建空的结算器。
func NewEngine() *Engine {
	return &Engine{
		bidders:  make(map[string]*bidder),
		bySeq:    make(map[int64]*bidder),
		auctions: make(map[int64]*auctionRec),
	}
}

// effectiveQ 计算连续未点击中标数为 f 时的有效质量分。
//
//	qe = max(1, floor(q*(100-10*min(max(f-2,0),5))/100))
func effectiveQ(q, f int64) int64 {
	d := f - 2
	if d < 0 {
		d = 0
	}
	if d > 5 {
		d = 5
	}
	factor := int64(100) - 10*d
	qe := q * factor / 100
	if qe < 1 {
		qe = 1
	}
	return qe
}

// Register 登记一个竞价者。
func (e *Engine) Register(id string, bid, q, budget int64) error {
	if id == "" || bid < 1 || bid > 1_000_000 || q < 1 || q > 1000 || budget < 1 || budget > 1_000_000_000_000 {
		return &OpError{Kind: ErrInvalidArgument}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.bidders[id]; ok {
		return &OpError{Kind: ErrBidderExists}
	}
	e.nextSeq++
	b := &bidder{id: id, bid: bid, q: q, budget: budget, initial: budget, seq: e.nextSeq}
	e.bidders[id] = b
	e.bySeq[b.seq] = b
	return nil
}

// participant 是参拍者在 Auction 时刻的快照。
type participant struct {
	b   *bidder
	qe  int64
	s   int64
	bid int64
}

// Auction 执行一次 GSP 拍卖：对全部参拍者只排序一次，取前 K 名。
func (e *Engine) Auction(k int, p int64) ([]Winner, error) {
	if k < 1 || k > 100 || p < 1 || p > 1_000_000 {
		return nil, &OpError{Kind: ErrInvalidArgument}
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	// 单次扫描选出参拍者：bid >= P 且可用预算 a = budget-h >= bid。
	part := make([]participant, 0, len(e.bidders))
	for _, b := range e.bidders {
		if b.bid < p {
			continue
		}
		if b.budget-b.h < b.bid {
			continue
		}
		qe := effectiveQ(b.q, b.f)
		part = append(part, participant{
			b:   b,
			qe:  qe,
			s:   b.bid * qe,
			bid: b.bid,
		})
	}
	if len(part) == 0 {
		return nil, &OpError{Kind: ErrNoParticipants}
	}

	// 对参拍者排且只排这一次序：分值 s 降序，同分登记序号小者在前。
	// sortCompares 记录排序期间的比较次数，用于验证不存在逐广告位的重复全量扫描。
	e.sortCompares = 0
	sorter := &participantSorter{part: part, engine: e}
	sort.Sort(sorter)

	n := k
	if n > len(part) {
		n = len(part)
	}

	e.nextAuc++
	auc := &auctionRec{id: e.nextAuc}
	winners := make([]Winner, 0, n)
	for j := 0; j < n; j++ {
		cur := part[j]
		var price int64
		if j+1 < len(part) {
			// 下一名参拍者（含第 K 名之后者）决定本名计价。
			nextS := part[j+1].s
			price = cur.bid
			gsp := nextS/cur.qe + 1
			if gsp < p {
				gsp = p
			}
			if gsp < price {
				price = gsp
			}
		} else {
			price = p
		}
		rec := &winRec{
			bidderSeq: cur.b.seq,
			id:        cur.b.id,
			p:         price,
			qe:        cur.qe,
		}
		auc.winners = append(auc.winners, rec)
		cur.b.h += price
		winners = append(winners, Winner{
			ID:  rec.id,
			P:   price,
			Qe:  cur.qe,
			Seq: cur.b.seq,
		})
	}
	e.auctions[auc.id] = auc
	return winners, nil
}

// Resolve 结算一次拍卖。
func (e *Engine) Resolve(auctionID int64, clicks []string) error {
	// 参数校验先于一切状态变更：点击集合不得含重复 id。
	seen := make(map[string]struct{}, len(clicks))
	for _, id := range clicks {
		if _, dup := seen[id]; dup {
			return &OpError{Kind: ErrInvalidArgument}
		}
		seen[id] = struct{}{}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	auc, ok := e.auctions[auctionID]
	if !ok {
		return &OpError{Kind: ErrAuctionNotFound}
	}
	if auc.resolved {
		return &OpError{Kind: ErrAuctionResolved}
	}
	winnerSet := make(map[string]*winRec, len(auc.winners))
	for _, w := range auc.winners {
		winnerSet[w.id] = w
	}
	for _, id := range clicks {
		if _, ok := winnerSet[id]; !ok {
			return &OpError{Kind: ErrClickNotWinner}
		}
	}

	clicked := make(map[string]bool, len(clicks))
	for _, id := range clicks {
		clicked[id] = true
	}
	for _, w := range auc.winners {
		b := e.bySeq[w.bidderSeq]
		w.clicked = clicked[w.id]
		// 无论是否点击，占用额都释放 p_j。
		b.h -= w.p
		if w.clicked {
			// 点击：真实扣预算，未点击中标计数清零，质量分正向回馈。
			b.budget -= w.p
			b.f = 0
			b.q = min64(1000, b.q+(1000-b.q)/8)
		} else {
			// 未点击：仅释放占用，计数加一，质量分负向回馈。
			b.f++
			b.q = max64(1, b.q-ceilDiv(b.q, 16))
		}
	}
	auc.resolved = true
	return nil
}

type participantSorter struct {
	part   []participant
	engine *Engine
}

func (s *participantSorter) Len() int { return len(s.part) }
func (s *participantSorter) Swap(i, j int) {
	s.part[i], s.part[j] = s.part[j], s.part[i]
}
func (s *participantSorter) Less(i, j int) bool {
	s.engine.sortCompares++
	if s.part[i].s != s.part[j].s {
		return s.part[i].s > s.part[j].s
	}
	return s.part[i].b.seq < s.part[j].b.seq
}

func ceilDiv(a, b int64) int64 { return (a + b - 1) / b }
func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
