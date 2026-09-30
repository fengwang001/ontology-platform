package idpool

import (
	"container/heap"
	"errors"
	"math"
	"sync"
)

// ErrInvalidConfig 在构造参数非法时返回。
var ErrInvalidConfig = errors.New("idpool: invalid config")

// ErrClockRollback 在调用时刻早于此前已接受时刻时返回。
var ErrClockRollback = errors.New("idpool: clock rollback")

// ErrOutOfRange 在释放的编号不属于 [1,N] 时返回。
var ErrOutOfRange = errors.New("idpool: id out of range")

// ErrFreeID 在释放空闲编号时返回。
var ErrFreeID = errors.New("idpool: id is free")

// ErrQuarantinedID 在释放隔离中的编号时返回。
var ErrQuarantinedID = errors.New("idpool: id is quarantined")

// ErrExhausted 表示所有编号均在使用中（无空闲也无隔离中编号）。
var ErrExhausted = errors.New("idpool: pool exhausted")

// ErrNoFree 表示当前没有空闲编号（存在隔离中的编号，或池已耗尽）。
var ErrNoFree = errors.New("idpool: no free id available")

// State 表示编号状态。
type State int

const (
	// StateFree 空闲，可立即分配。
	StateFree State = iota
	// StateInUse 使用中。
	StateInUse
	// StateQuarantined 隔离中，需等待解除时刻。
	StateQuarantined
)

// Pool 是带隔离期与短命加倍的编号池。
type Pool struct {
	mu        sync.Mutex
	n         int
	baseQ     int64
	maxQ      int64
	threshold int64
	lastSeen  int64

	states     []State // 下标 0 未使用，编号 i 对应 states[i]
	allocTimes []int64
	lastQ      []int64

	freeHeap       intHeap
	quarantineHeap readyHeap
}

// New 创建编号 1..N 的编号池。
//
// baseQ 为基础隔离期 Q；maxQ 为隔离上限 Qmax（Qmax >= Q）；
// shortLivedThresholdR 为短命阈值 R（整数毫秒，存活时间 < R 才算短命，恰好等于 R 为正常寿命）。
func New(n int, baseQ, maxQ, shortLivedThresholdR int64) (*Pool, error) {
	if n <= 0 || baseQ <= 0 || maxQ < baseQ || shortLivedThresholdR <= 0 {
		return nil, ErrInvalidConfig
	}
	p := &Pool{
		n:          n,
		baseQ:      baseQ,
		maxQ:       maxQ,
		threshold:  shortLivedThresholdR,
		lastSeen:   math.MinInt64,
		states:     make([]State, n+1),
		allocTimes: make([]int64, n+1),
		lastQ:      make([]int64, n+1),
	}
	for id := 1; id <= n; id++ {
		p.states[id] = StateFree
		p.lastQ[id] = baseQ // 新编号「上一次隔离期」初值为 Q
		heap.Push(&p.freeHeap, id)
	}
	return p, nil
}

// Allocate 在时刻 now 分配最小的空闲编号。
// 隔离中的编号恰在解除时刻（readyAt == now）即可分配；绝不提前借出。
func (p *Pool) Allocate(now int64) (id int, err error) {
	id, _, err = p.AllocateDetailed(now)
	return id, err
}

// AllocationFailure 描述无空闲编号时的失败详情。
type AllocationFailure struct {
	// EarliestReady 为隔离中编号最早的解除时刻；无隔离编号时为 0。
	EarliestReady int64
	// EarliestID 为最早解除的编号（并列取小）；无隔离编号时为 0。
	EarliestID int
	// Exhausted 为 true 表示所有编号均在使用中。
	Exhausted bool
}

// AllocateDetailed 与 Allocate 相同，失败时额外给出最早解除信息。
func (p *Pool) AllocateDetailed(now int64) (id int, failure AllocationFailure, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if now < p.lastSeen {
		return 0, AllocationFailure{}, ErrClockRollback
	}
	p.lastSeen = now
	p.expireLocked(now)

	if p.freeHeap.Len() > 0 {
		id = heap.Pop(&p.freeHeap).(int)
		p.states[id] = StateInUse
		p.allocTimes[id] = now
		return id, AllocationFailure{}, nil
	}

	if p.quarantineHeap.Len() > 0 {
		top := p.quarantineHeap[0]
		failure = AllocationFailure{
			EarliestReady: top.readyAt,
			EarliestID:    top.id,
			Exhausted:     false,
		}
		return 0, failure, ErrNoFree
	}
	return 0, AllocationFailure{Exhausted: true}, ErrExhausted
}

// Release 在时刻 now 释放使用中的编号 id。
//
// 隔离期取法：存活时间 now-allocTime < R 时取 min(2*上一次隔离期, Qmax)，
// 否则取 Q；恰好等于 R 按正常寿命处理。编号在 now+隔离期 起变为空闲。
func (p *Pool) Release(id int, now int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	// 时钟回拨优先判定。
	if now < p.lastSeen {
		return ErrClockRollback
	}
	p.lastSeen = now
	p.expireLocked(now)

	// 其余情形按「越界、空闲、隔离中」的顺序只报第一个。
	if id < 1 || id > p.n {
		return ErrOutOfRange
	}
	switch p.states[id] {
	case StateFree:
		return ErrFreeID
	case StateQuarantined:
		return ErrQuarantinedID
	}

	life := now - p.allocTimes[id]
	q := p.baseQ
	if life < p.threshold {
		q = p.lastQ[id] * 2
		if q > p.maxQ || q < 0 { // q < 0 防御翻倍溢出
			q = p.maxQ
		}
	}
	p.lastQ[id] = q
	p.states[id] = StateQuarantined
	heap.Push(&p.quarantineHeap, readyEntry{readyAt: now + q, id: id})
	return nil
}

// Query 返回时刻 now 的池快照。
type Snapshot struct {
	InUse         []int
	Quarantined   []int
	Free          []int
	EarliestReady int64
	EarliestID    int
}

// Query 查看时刻 now 的池状态（不改变任何状态）。
func (p *Pool) Query(now int64) (Snapshot, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if now < p.lastSeen {
		return Snapshot{}, ErrClockRollback
	}
	p.lastSeen = now
	p.expireLocked(now)

	snap := Snapshot{}
	for id := 1; id <= p.n; id++ {
		switch p.states[id] {
		case StateInUse:
			snap.InUse = append(snap.InUse, id)
		case StateQuarantined:
			snap.Quarantined = append(snap.Quarantined, id)
		case StateFree:
			snap.Free = append(snap.Free, id)
		}
	}
	if p.quarantineHeap.Len() > 0 {
		top := p.quarantineHeap[0]
		snap.EarliestReady = top.readyAt
		snap.EarliestID = top.id
	}
	return snap, nil
}

// expireLocked 将隔离期已到（readyAt <= now）的编号转为空闲。
func (p *Pool) expireLocked(now int64) {
	for p.quarantineHeap.Len() > 0 && p.quarantineHeap[0].readyAt <= now {
		top := heap.Pop(&p.quarantineHeap).(readyEntry)
		p.states[top.id] = StateFree
		heap.Push(&p.freeHeap, top.id)
	}
}

// intHeap 是编号的最小堆。
type intHeap []int

func (h intHeap) Len() int           { return len(h) }
func (h intHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h intHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *intHeap) Push(x any)        { *h = append(*h, x.(int)) }
func (h *intHeap) Pop() any {
	old := *h
	last := len(old) - 1
	x := old[last]
	*h = old[:last]
	return x
}

// readyEntry 记录隔离中编号及其解除时刻。
type readyEntry struct {
	readyAt int64
	id      int
}

// readyHeap 按解除时刻升序，并列时编号小者优先。
type readyHeap []readyEntry

func (h readyHeap) Len() int { return len(h) }
func (h readyHeap) Less(i, j int) bool {
	if h[i].readyAt != h[j].readyAt {
		return h[i].readyAt < h[j].readyAt
	}
	return h[i].id < h[j].id
}
func (h readyHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *readyHeap) Push(x any)   { *h = append(*h, x.(readyEntry)) }
func (h *readyHeap) Pop() any {
	old := *h
	last := len(old) - 1
	x := old[last]
	*h = old[:last]
	return x
}
