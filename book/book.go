// Package book 维护集合竞价委托簿：可撤/不可撤阶段、校验与开盘。
//
// 拒绝按次序只报第一个：参数非法 > 时钟回退 > 阶段不符 > 编号重复/不存在 >
// 价格越带 > 账户超限。被拒绝的操作不改任何状态（含时钟与序号）。
// 所有方法可并发调用，效果等价于某个串行顺序。
package book

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"ontology/alloc"
	"ontology/auction"
)

const (
	maxPrice = int64(1_000_000_000)
	maxQty   = int64(1_000_000_000)
	maxTime  = int64(1_000_000_000_000)
	maxLimit = int64(100_000)
)

// 拒绝原因，可用 errors.Is 区分。
var (
	ErrParam        = errors.New("book: invalid parameter")
	ErrClock        = errors.New("book: clock regression")
	ErrPhase        = errors.New("book: phase violation")
	ErrDuplicateID  = errors.New("book: duplicate order id")
	ErrUnknownID    = errors.New("book: unknown order id")
	ErrPriceBand    = errors.New("book: price outside band")
	ErrAccountLimit = errors.New("book: account order limit reached")
)

// Trade 是一笔开盘成交，成交价一律为开盘价。
type Trade = alloc.Trade

// Order 是剩余清单中的委托（Qty 为剩余量）。
type Order struct {
	Seq   int
	ID    string
	Acct  string
	Buy   bool
	Price int64
	Qty   int64
}

// Book 是集合竞价委托簿。
type Book struct {
	mu     sync.Mutex
	ref    int64
	tc     int64
	tu     int64
	lo     int64
	hi     int64
	m      int64
	clock  int64
	seq    int
	opened bool
	live   map[string]*Order // 在簿委托（按 id）
	seen   map[string]bool   // 所有被接受过的 id
	byAcct map[string]int64  // 账户在簿委托数
}

// New 创建委托簿：1<=lo<=ref<=hi<=1e9，0<=tc<=tu<=1e12，1<=m<=1e5。
func New(ref, tc, tu, lo, hi, m int64) (*Book, error) {
	if ref < 1 || ref > maxPrice || lo < 1 || hi > maxPrice || lo > ref || ref > hi {
		return nil, fmt.Errorf("%w: ref/lo/hi", ErrParam)
	}
	if tc < 0 || tu > maxTime || tc > tu {
		return nil, fmt.Errorf("%w: tc/tu", ErrParam)
	}
	if m < 1 || m > maxLimit {
		return nil, fmt.Errorf("%w: m", ErrParam)
	}
	return &Book{
		ref:    ref,
		tc:     tc,
		tu:     tu,
		lo:     lo,
		hi:     hi,
		m:      m,
		live:   make(map[string]*Order),
		seen:   make(map[string]bool),
		byAcct: make(map[string]int64),
	}, nil
}

func validNow(now int64) bool { return now >= 0 && now <= maxTime }

// Submit 报单；buy 为买卖方向。被接受的委托按次序获得序号 1,2,3...。
func (b *Book) Submit(now int64, id, acct string, buy bool, price, qty int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !validNow(now) || id == "" || acct == "" ||
		price < 1 || price > maxPrice || qty < 1 || qty > maxQty {
		return fmt.Errorf("%w: submit", ErrParam)
	}
	if now < b.clock {
		return fmt.Errorf("%w: now=%d clock=%d", ErrClock, now, b.clock)
	}
	if b.opened {
		return fmt.Errorf("%w: already opened", ErrPhase)
	}
	if b.seen[id] {
		return fmt.Errorf("%w: %q", ErrDuplicateID, id)
	}
	if price < b.lo || price > b.hi {
		return fmt.Errorf("%w: price=%d band=[%d,%d]", ErrPriceBand, price, b.lo, b.hi)
	}
	if b.byAcct[acct] >= b.m {
		return fmt.Errorf("%w: acct=%q m=%d", ErrAccountLimit, acct, b.m)
	}
	b.seq++
	b.live[id] = &Order{Seq: b.seq, ID: id, Acct: acct, Buy: buy, Price: price, Qty: qty}
	b.seen[id] = true
	b.byAcct[acct]++
	b.clock = now
	return nil
}

// Cancel 撤单，仅可撤期（now<Tc）允许；撤销后释放账户名额。
func (b *Book) Cancel(now int64, id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !validNow(now) || id == "" {
		return fmt.Errorf("%w: cancel", ErrParam)
	}
	if now < b.clock {
		return fmt.Errorf("%w: now=%d clock=%d", ErrClock, now, b.clock)
	}
	if b.opened {
		return fmt.Errorf("%w: already opened", ErrPhase)
	}
	if now >= b.tc {
		return fmt.Errorf("%w: cancel in no-cancel phase", ErrPhase)
	}
	o, ok := b.live[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownID, id)
	}
	delete(b.live, id)
	b.byAcct[o.Acct]--
	b.clock = now
	return nil
}

// Uncross 开盘：要求 now>=Tu，只能成功一次。
// 返回开盘价（无成交时为 0）、按产生次序的成交清单与按序号升序的剩余清单。
func (b *Book) Uncross(now int64) (int64, []Trade, []Order, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !validNow(now) {
		return 0, nil, nil, fmt.Errorf("%w: uncross", ErrParam)
	}
	if now < b.clock {
		return 0, nil, nil, fmt.Errorf("%w: now=%d clock=%d", ErrClock, now, b.clock)
	}
	if b.opened {
		return 0, nil, nil, fmt.Errorf("%w: already opened", ErrPhase)
	}
	if now < b.tu {
		return 0, nil, nil, fmt.Errorf("%w: uncross before tu", ErrPhase)
	}
	b.opened = true
	b.clock = now

	snap := make([]auction.Order, 0, len(b.live))
	for _, o := range b.live {
		snap = append(snap, auction.Order{Buy: o.Buy, Price: o.Price, Qty: o.Qty})
	}
	price := auction.Price(snap, b.ref)
	if price == 0 {
		return 0, nil, b.remaining(), nil
	}
	aorders := make([]alloc.Order, 0, len(b.live))
	for _, o := range b.live {
		aorders = append(aorders, alloc.Order{Seq: o.Seq, ID: o.ID, Buy: o.Buy, Price: o.Price, Qty: o.Qty})
	}
	trades, arem := alloc.Allocate(aorders, price)
	acctOf := make(map[int]string, len(b.live))
	for _, o := range b.live {
		acctOf[o.Seq] = o.Acct
	}
	remaining := make([]Order, 0, len(arem))
	for _, o := range arem {
		remaining = append(remaining, Order{
			Seq: o.Seq, ID: o.ID, Acct: acctOf[o.Seq], Buy: o.Buy, Price: o.Price, Qty: o.Qty,
		})
	}
	return price, trades, remaining, nil
}

// remaining 返回全部在簿委托（无成交时），按序号升序。
func (b *Book) remaining() []Order {
	out := make([]Order, 0, len(b.live))
	for _, o := range b.live {
		out = append(out, *o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out
}
