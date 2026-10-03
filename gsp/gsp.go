// Package gsp 实现带质量分加权、底价、预算占用与质量回馈的
// 广义第二价格（GSP）广告位竞价结算器。
package gsp

import (
	"errors"
	"sort"
	"sync"
)

// 拒绝原因，按此优先级只报第一个。
var (
	ErrInvalidParam     = errors.New("gsp: 参数非法")
	ErrBidderExists     = errors.New("gsp: 竞价者已存在")
	ErrAuctionNotFound  = errors.New("gsp: 拍卖不存在")
	ErrAuctionResolved  = errors.New("gsp: 拍卖已结算")
	ErrNonWinnerClicked = errors.New("gsp: 点击集合含非赢家")
	ErrNoParticipants   = errors.New("gsp: 无参拍者")
)

// Winner 表示一个赢家的名次结果。
type Winner struct {
	ID    string
	Price int64 // 每点击计价 p_j
}

type bidder struct {
	id     string
	bid    int64
	q      int64
	budget int64
	h      int64 // 占用额（未结算拍卖的 p_j 之和）
	seq    int64 // 登记序号，从 1 递增
	f      int64 // 连续未点击中标数
}

// effectiveQuality 有效质量分：
// qe = max(1, floor(q*(100-10*min(max(f-2,0),5))/100))
func effectiveQuality(q, f int64) int64 {
	d := f - 2
	if d < 0 {
		d = 0
	}
	if d > 5 {
		d = 5
	}
	qe := q * (100 - 10*d) / 100
	if qe < 1 {
		qe = 1
	}
	return qe
}

type winnerRecord struct {
	id    string
	price int64
	qe    int64 // Auction 时刻冻结的有效质量分
}

type auction struct {
	id       int64
	k        int
	p        int64
	winners  []winnerRecord
	resolved bool
}

// Engine 是竞价结算器，所有方法可并发调用，
// 结果等价于某个串行顺序。
type Engine struct {
	mu          sync.Mutex
	bidders     map[string]*bidder
	seq         int64 // 已登记竞价者数
	nextAuction int64 // 下一个拍卖号
	auctions    map[int64]*auction

	sortCalls   int64 // 非导出计数器：Auction 中对参拍者排序的次数
	scannedBids int64 // 非导出计数器：Auction 中扫描竞价者的次数
}

// New 创建一个空的结算器。
func New() *Engine {
	return &Engine{
		bidders:     make(map[string]*bidder),
		auctions:    make(map[int64]*auction),
		nextAuction: 1,
	}
}

// Register 登记竞价者。
func (e *Engine) Register(id string, bid, q, budget int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == "" || bid < 1 || bid > 1_000_000 ||
		q < 1 || q > 1000 || budget < 1 || budget > 1_000_000_000_000 {
		return ErrInvalidParam
	}
	if _, ok := e.bidders[id]; ok {
		return ErrBidderExists
	}
	e.seq++
	e.bidders[id] = &bidder{id: id, bid: bid, q: q, budget: budget, seq: e.seq}
	return nil
}

// participant 是 Auction 时刻参拍者的快照。
type participant struct {
	b  *bidder
	qe int64
	s  int64 // 分值 s = bid * qe
}

// Auction 举行一次拍卖，返回按名次的赢家清单。
func (e *Engine) Auction(k int, p int64) ([]Winner, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if k < 1 || k > 100 || p < 1 || p > 1_000_000 {
		return nil, ErrInvalidParam
	}

	// 收集参拍者：bid >= P 且可用预算 a = budget - h >= bid。
	parts := make([]participant, 0, len(e.bidders))
	for _, b := range e.bidders {
		e.scannedBids++
		if b.bid >= p && b.budget-b.h >= b.bid {
			qe := effectiveQuality(b.q, b.f)
			parts = append(parts, participant{b: b, qe: qe, s: b.bid * qe})
		}
	}
	if len(parts) == 0 {
		return nil, ErrNoParticipants
	}

	// 只对参拍者排一次序：分值降序，同分登记序号小者在前。
	e.sortCalls++
	sort.Slice(parts, func(i, j int) bool {
		if parts[i].s != parts[j].s {
			return parts[i].s > parts[j].s
		}
		return parts[i].b.seq < parts[j].b.seq
	})

	n := k
	if n > len(parts) {
		n = len(parts)
	}
	auc := &auction{id: e.nextAuction, k: k, p: p}
	e.nextAuction++
	winners := make([]Winner, 0, n)
	for i := 0; i < n; i++ {
		price := p
		if i+1 < len(parts) {
			// p_j = min(bid_j, max(P, floor(s_next/qe_j)+1))
			price = parts[i+1].s/parts[i].qe + 1
			if price < p {
				price = p
			}
			if price > parts[i].b.bid {
				price = parts[i].b.bid
			}
		}
		parts[i].b.h += price
		auc.winners = append(auc.winners, winnerRecord{id: parts[i].b.id, price: price, qe: parts[i].qe})
		winners = append(winners, Winner{ID: parts[i].b.id, Price: price})
	}
	e.auctions[auc.id] = auc
	return winners, nil
}

// Resolve 结算一个拍卖：clicked 为该拍卖赢家 id 的子集（可为空）。
func (e *Engine) Resolve(auctionID int64, clicked []string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// 参数非法：点击集合含重复 id。
	seen := make(map[string]struct{}, len(clicked))
	for _, id := range clicked {
		if _, dup := seen[id]; dup {
			return ErrInvalidParam
		}
		seen[id] = struct{}{}
	}

	auc, ok := e.auctions[auctionID]
	if !ok {
		return ErrAuctionNotFound
	}
	if auc.resolved {
		return ErrAuctionResolved
	}
	winnerSet := make(map[string]struct{}, len(auc.winners))
	for _, w := range auc.winners {
		winnerSet[w.id] = struct{}{}
	}
	for _, id := range clicked {
		if _, ok := winnerSet[id]; !ok {
			return ErrNonWinnerClicked
		}
	}

	clickedSet := seen
	for _, w := range auc.winners {
		b := e.bidders[w.id]
		b.h -= w.price
		if _, ok := clickedSet[w.id]; ok {
			b.budget -= w.price
			b.f = 0
			b.q += (1000 - b.q) / 8
			if b.q > 1000 {
				b.q = 1000
			}
		} else {
			b.f++
			b.q -= (b.q + 15) / 16
			if b.q < 1 {
				b.q = 1
			}
		}
	}
	auc.resolved = true
	return nil
}
