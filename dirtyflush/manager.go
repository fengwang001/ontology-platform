// Package dirtyflush 维护一个按首次弄脏 LSN（oldest）排序的脏页刷写链表，
// 以及页与页之间的刷写先后依赖（有向无环图）。
//
// 所有操作都可并发调用；内部以单一互斥锁串行化，结果等价于某个串行执行
// 顺序。被拒绝的操作不改变任何状态。
package dirtyflush

import (
	"errors"
	"sync"
)

// 页号与 LSN 的合法闭区间。
const (
	MinPage = 0
	MaxPage = 1_000_000
	MinLSN  = 1
	MaxLSN  = 1_000_000_000_000_000
	MaxCap  = 1_000_000
)

// RejectReason 标识操作被拒绝的具体原因，便于调用方精确区分。
type RejectReason int

const (
	ReasonInvalidArg       RejectReason = iota + 1 // 参数非法（越界、a==b 等）
	ReasonLSNTooSmall                              // Modify 的 lsn 不够大 / SetFlushed 回退
	ReasonDirtyFull                                // 干净页要变脏但脏页数已达 D
	ReasonPageNotDirty                             // FlushStart：页不脏
	ReasonPageInFlight                             // FlushStart：页已在途
	ReasonLogNotDurable                            // FlushStart：p.lsn 大于已落盘 LSN
	ReasonPredecessorDirty                         // FlushStart：存在仍为脏的前置页
	ReasonNotInFlight                              // FlushDone：页不在途
	ReasonCycle                                    // AddDep：登记后会成环
)

// OpError 携带被拒绝操作的原因，以及便于排查的上下文。
type OpError struct {
	Reason RejectReason
	Op     string
	Msg    string
}

func (e *OpError) Error() string {
	return e.Op + ": " + e.Msg
}

func opErr(op string, reason RejectReason, msg string) *OpError {
	return &OpError{Reason: reason, Op: op, Msg: msg}
}

// ErrReject 用于 errors.Is 判断“某操作是否被规则拒绝”。
var ErrReject = errors.New("dirtyflush: operation rejected")

func (e *OpError) Unwrap() error { return ErrReject }

// Page 是单个页的可观测状态快照。
type Page struct {
	ID         int
	LSN        int64 // p.lsn：最新一次被接受的 Modify LSN
	Oldest     int64 // 首次弄脏 LSN（在途结束且期间被改过则为 firstAfter）
	Dirty      bool
	InFlight   bool  // 是否有刷写正在进行
	FirstAfter int64 // 在途期间第一次修改的 LSN；0 表示尚无
}

type pageState struct {
	lsn        int64
	oldest     int64
	dirty      bool
	inFlight   bool
	firstAfter int64
	snap       int64 // FlushStart 时记录的 p.lsn 快照
}

// Manager 是缓冲池刷写管理器。零值不可用，请用 New 构造。
type Manager struct {
	mu     sync.Mutex
	cap    int
	pages  []pageState
	order  *treap
	dirtyN int

	// 依赖图：out[a] 包含所有 a→b 的边；in[b] 包含所有 q→b 的前置 q。
	// 不变量：边只从脏页发出，且整个图无环。
	out map[int]map[int]struct{}
	in  map[int]map[int]struct{}

	maxModify int64 // 已接受的最大 Modify LSN
	flushed   int64 // 日志已落盘水位
	touched   map[int]struct{}
}

// New 创建容量（脏页上限）为 D 的管理器。
func New(D int) (*Manager, error) {
	if D < 1 || D > MaxCap {
		return nil, opErr("New", ReasonInvalidArg, "D out of range")
	}
	return &Manager{
		cap:     D,
		pages:   make([]pageState, MaxPage+1),
		order:   newTreap(),
		out:     make(map[int]map[int]struct{}),
		in:      make(map[int]map[int]struct{}),
		touched: make(map[int]struct{}),
	}, nil
}

// Modify 记录页 p 在 lsn 处发生修改。
func (m *Manager) Modify(p int, lsn int64) error {
	if p < MinPage || p > MaxPage || lsn < MinLSN || lsn > MaxLSN {
		return opErr("Modify", ReasonInvalidArg, "page or lsn out of range")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if lsn <= m.maxModify {
		return opErr("Modify", ReasonLSNTooSmall, "lsn must be strictly greater than previous max modify lsn")
	}
	st := &m.pages[p]
	if !st.dirty && m.dirtyN >= m.cap {
		return opErr("Modify", ReasonDirtyFull, "dirty page limit reached")
	}
	m.maxModify = lsn
	st.lsn = lsn
	m.touched[p] = struct{}{}
	switch {
	case !st.dirty:
		// 干净页变脏：first dirty LSN 就是本次 lsn，排到链表末尾。
		st.dirty = true
		st.oldest = lsn
		m.order.insert(p, lsn)
		m.dirtyN++
	case st.inFlight:
		// 在途期间被再次修改；只记录第一次。
		if st.firstAfter == 0 {
			st.firstAfter = lsn
		}
	}
	return nil
}

// SetFlushed 抬高日志已落盘水位。
func (m *Manager) SetFlushed(l int64) error {
	if l < 0 || l > MaxLSN {
		return opErr("SetFlushed", ReasonInvalidArg, "water mark out of range")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if l < m.flushed {
		return opErr("SetFlushed", ReasonLSNTooSmall, "flushed lsn must not move backwards")
	}
	m.flushed = l
	return nil
}

// FlushStart 开始刷写页 p，返回本次刷写的快照 LSN。
func (m *Manager) FlushStart(p int) (int64, error) {
	if p < MinPage || p > MaxPage {
		return 0, opErr("FlushStart", ReasonInvalidArg, "page out of range")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st := &m.pages[p]
	if !st.dirty {
		return 0, opErr("FlushStart", ReasonPageNotDirty, "page is not dirty")
	}
	if st.inFlight {
		return 0, opErr("FlushStart", ReasonPageInFlight, "page already flushing")
	}
	if st.lsn > m.flushed {
		return 0, opErr("FlushStart", ReasonLogNotDurable, "page lsn is beyond flushed log water mark")
	}
	for q := range m.in[p] {
		if m.pages[q].dirty {
			return 0, opErr("FlushStart", ReasonPredecessorDirty, "predecessor page still dirty")
		}
	}
	st.inFlight = true
	st.snap = st.lsn
	return st.snap, nil
}

// FlushDone 结束页 p 的在途刷写。
func (m *Manager) FlushDone(p int) error {
	if p < MinPage || p > MaxPage {
		return opErr("FlushDone", ReasonInvalidArg, "page out of range")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st := &m.pages[p]
	if !st.inFlight {
		return opErr("FlushDone", ReasonNotInFlight, "page has no flush in flight")
	}
	if st.lsn == st.snap {
		// 刷写期间没有新修改：页变干净，离开链表，删除所有出边。
		m.order.erase(p, st.oldest)
		st.dirty = false
		st.inFlight = false
		st.oldest = 0
		st.firstAfter = 0
		st.snap = 0
		m.dirtyN--
		m.removeOutEdges(p)
		return nil
	}
	// 刷写期间又被修改：仍为脏，按 firstAfter 作为新的 oldest 插回。
	old := st.oldest
	m.order.erase(p, old)
	st.inFlight = false
	st.oldest = st.firstAfter
	st.firstAfter = 0
	st.snap = 0
	m.order.insert(p, st.oldest)
	return nil
}
