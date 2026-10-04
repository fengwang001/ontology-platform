// Package pool 实现融券券源池与出借人召回处理器的入口。
package pool

import (
	"errors"
	"sync"

	"ontology/loan"
	"ontology/recall"
)

// 可区分的拒绝原因。
var (
	// ErrInvalid 为参数非法（含 New 的 N、pen 非法）。
	ErrInvalid = errors.New("pool: invalid argument")
	// ErrClockBack 为时钟回退。
	ErrClockBack = errors.New("pool: clock moved backwards")
	// ErrNotFound 为不存在（未定价/未登记/无欠券）。
	ErrNotFound = errors.New("pool: not found")
	// ErrOverQty 为 Withdraw/Return 超量。
	ErrOverQty = errors.New("pool: quantity exceeds available")
	// ErrInsufficient 为 Borrow 券源不足。
	ErrInsufficient = errors.New("pool: insufficient securities")
)

// Buyin 是对外暴露的强制买入记录。
type Buyin = recall.Buyin

// Contract 是对外暴露的合约视图。
type Contract = loan.Contract

const (
	maxNow = 1_000_000_000_000
	maxQty = 1_000_000_000
	maxPen = 10_000
)

// idleHeap 按登记序号升序保存“可能有空闲”的出借人；懒删除。
type idleEntry struct {
	regSeq int64
	lender loan.ID
}

type idleHeap []idleEntry

func (h idleHeap) Len() int           { return len(h) }
func (h idleHeap) Less(i, j int) bool { return h[i].regSeq < h[j].regSeq }
func (h idleHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *idleHeap) Push(x any)        { *h = append(*h, x.(idleEntry)) }
func (h *idleHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

type symState struct {
	priced bool
	price  int64
	idle   idleHeap
}

// Pool 是券源池；零值不可用，须用 New 创建。
type Pool struct {
	mu      sync.Mutex
	now     int64
	n       int64
	pen     int64
	book    *loan.Book
	sched   *recall.Scheduler
	nextID  int64
	syms    map[loan.ID]*symState
	payable map[loan.ID]int64

	// scanned 为最近一次真正分配的 Borrow 所扫描的、当时空闲>0 的出借人数。
	scanned int
}

// New 创建召回通知期 N、罚金率 pen（万分比）的券源池。
func New(n, pen int64) (*Pool, error) {
	if n < 1 || n > 1_000_000 || pen < 0 || pen > maxPen {
		return nil, ErrInvalid
	}
	return &Pool{
		n:       n,
		pen:     pen,
		book:    loan.NewBook(),
		sched:   recall.New(),
		syms:    make(map[loan.ID]*symState),
		payable: make(map[loan.ID]int64),
	}, nil
}

func validID(b []byte) bool { return len(b) > 0 }
func validQty(q int64) bool { return q >= 1 && q <= maxQty }
func validNow(t int64) bool { return t >= 0 && t <= maxNow }

func (p *Pool) sym(sym loan.ID) *symState {
	s := p.syms[sym]
	if s == nil {
		s = &symState{}
		p.syms[sym] = s
	}
	return s
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
