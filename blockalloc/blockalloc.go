// Package blockalloc assigns subscriber port blocks and session ports out of
// a public address pool, with lazy time-pure block release and NAT logging.
package blockalloc

import (
	"container/heap"
	"errors"
	"sync"

	"ontology/natlog"
	"ontology/pubpool"
)

var (
	ErrInvalidParam  = errors.New("blockalloc: invalid parameter")
	ErrClock         = errors.New("blockalloc: clock regression")
	ErrBlockLimit    = errors.New("blockalloc: subscriber block limit exceeded")
	ErrAddrDrained   = errors.New("blockalloc: bound address drained")
	ErrAddrExhausted = errors.New("blockalloc: bound address exhausted")
	ErrPoolExhausted = errors.New("blockalloc: pool exhausted")
	ErrNoSession     = errors.New("blockalloc: no such session")
)

const (
	maxSub = 1_000_000_000
	maxNow = 1_000_000_000_000
	maxT   = 1_000_000_000
)

type subRecord struct {
	addr   int
	blocks [][2]int // (address, block index) in allocation order
}

type relItem struct {
	at        int64 // release time = idleSince + T
	idleSince int64
	addr      int
	idx       int
}

type relHeap []relItem

func (h relHeap) Len() int { return len(h) }
func (h relHeap) Less(i, j int) bool {
	if h[i].at != h[j].at {
		return h[i].at < h[j].at
	}
	if h[i].addr != h[j].addr {
		return h[i].addr < h[j].addr
	}
	return h[i].idx < h[j].idx
}
func (h relHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *relHeap) Push(x interface{}) { *h = append(*h, x.(relItem)) }
func (h *relHeap) Pop() interface{} {
	old := *h
	v := old[len(old)-1]
	*h = old[:len(old)-1]
	return v
}

// Allocator hands out port blocks and session ports. All methods are safe
// for concurrent use; the result equals some serial order of the calls.
type Allocator struct {
	mu      sync.Mutex
	pool    *pubpool.Pool
	m       int
	t       int64
	subs    map[int]*subRecord
	log     *natlog.Log
	lastNow int64
	rel     relHeap
	probes  int
}

// New validates the parameters and builds an allocator.
func New(A, L, H, S, M int, T int64) (*Allocator, error) {
	if M < 1 || M > 8 || T < 0 || T > maxT {
		return nil, ErrInvalidParam
	}
	p, err := pubpool.NewPool(A, L, H, S)
	if err != nil {
		return nil, ErrInvalidParam
	}
	return &Allocator{
		pool:    p,
		m:       M,
		t:       T,
		subs:    make(map[int]*subRecord),
		log:     natlog.NewLog(A, L, H, S),
		lastNow: -1,
	}, nil
}

// Log returns the allocation log.
func (a *Allocator) Log() *natlog.Log { return a.log }

// Lookup answers who held (addr, port) at time t, reading landed log only.
func (a *Allocator) Lookup(addr, port int, t int64) (int, error) {
	return a.log.Lookup(addr, port, t)
}

// collect pops the idle blocks whose release time is at most now, in
// (release time, address, block) order. Stale heap entries are dropped.
func (a *Allocator) collect(now int64) []relItem {
	var out []relItem
	for len(a.rel) > 0 && a.rel[0].at <= now {
		it := heap.Pop(&a.rel).(relItem)
		blk := &a.pool.Addrs[it.addr].Blocks[it.idx]
		if blk.Idle && blk.IdleSince == it.idleSince {
			out = append(out, it)
		}
	}
	return out
}

func (a *Allocator) requeue(items []relItem) {
	for _, it := range items {
		heap.Push(&a.rel, it)
	}
}

// applyRelease lands one release: FREE log, subscriber unlink, block freed.
func (a *Allocator) applyRelease(it relItem) {
	ad := &a.pool.Addrs[it.addr]
	blk := &ad.Blocks[it.idx]
	sub := blk.Owner
	a.log.Append(natlog.Free, sub, it.addr, a.pool.BlockFirst(it.idx), a.pool.BlockLast(it.idx), it.at)
	rec := a.subs[sub]
	for i, b := range rec.blocks {
		if b == [2]int{it.addr, it.idx} {
			rec.blocks = append(rec.blocks[:i], rec.blocks[i+1:]...)
			break
		}
	}
	if len(rec.blocks) == 0 {
		delete(a.subs, sub)
		ad.Bound--
	}
	blk.Reset()
	heap.Push(&ad.Free, it.idx)
}

type openPlan struct {
	reuse bool
	addr  int
	idx   int
	err   error
}

// decideOpen decides on the hypothetical state where pend is already
// released. It only examines the subscriber record, its blocks, the address
// records and the chosen block, so probes stays within M+A+K.
func (a *Allocator) decideOpen(sub int, pend []relItem) openPlan {
	pset := make(map[[2]int]bool, len(pend))
	pendAddr := make(map[int]int)
	pendSub := make(map[int]int)
	for _, it := range pend {
		pset[[2]int{it.addr, it.idx}] = true
		pendAddr[it.addr]++
		pendSub[a.pool.Addrs[it.addr].Blocks[it.idx].Owner]++
	}
	a.probes++
	rec := a.subs[sub]
	var eff [][2]int
	if rec != nil {
		for _, b := range rec.blocks {
			if !pset[b] {
				eff = append(eff, b)
			}
		}
	}
	for _, b := range eff {
		blk := &a.pool.Addrs[b[0]].Blocks[b[1]]
		a.probes++
		if blk.Used < a.pool.S {
			return openPlan{reuse: true, addr: b[0], idx: b[1]}
		}
	}
	if len(eff) >= a.m {
		return openPlan{err: ErrBlockLimit}
	}
	if len(eff) > 0 {
		addr := rec.addr
		ad := &a.pool.Addrs[addr]
		a.probes++
		if ad.Drained {
			return openPlan{err: ErrAddrDrained}
		}
		if len(ad.Free) == 0 && pendAddr[addr] == 0 {
			return openPlan{err: ErrAddrExhausted}
		}
		a.probes++
		return openPlan{addr: addr, idx: -1}
	}
	released := make(map[int]int) // address -> subscribers fully released
	for s, n := range pendSub {
		if r := a.subs[s]; r != nil && len(r.blocks) == n {
			released[r.addr]++
		}
	}
	best, bestBound := -1, 0
	for i := range a.pool.Addrs {
		ad := &a.pool.Addrs[i]
		a.probes++
		if ad.Drained || (len(ad.Free) == 0 && pendAddr[i] == 0) {
			continue
		}
		if bound := ad.Bound - released[i]; best == -1 || bound < bestBound {
			best, bestBound = i, bound
		}
	}
	if best == -1 {
		return openPlan{err: ErrPoolExhausted}
	}
	a.probes++
	return openPlan{addr: best, idx: -1}
}

// execOpen runs an accepted plan on the post-release state.
func (a *Allocator) execOpen(sub int, now int64, plan openPlan) (int, int, error) {
	ad := &a.pool.Addrs[plan.addr]
	if plan.reuse {
		blk := &ad.Blocks[plan.idx]
		blk.Idle = false
		off := blk.FirstFree()
		blk.Set(off)
		blk.Used++
		return plan.addr, a.pool.BlockFirst(plan.idx) + off, nil
	}
	idx := heap.Pop(&ad.Free).(int)
	blk := &ad.Blocks[idx]
	blk.Owner = sub
	blk.Used = 1
	blk.Set(0)
	rec := a.subs[sub]
	if rec == nil {
		rec = &subRecord{addr: plan.addr}
		a.subs[sub] = rec
		ad.Bound++
	}
	rec.blocks = append(rec.blocks, [2]int{plan.addr, idx})
	first := a.pool.BlockFirst(idx)
	a.log.Append(natlog.Alloc, sub, plan.addr, first, a.pool.BlockLast(idx), now)
	return plan.addr, first, nil
}

// Open allocates one session port for sub at logical time now.
func (a *Allocator) Open(sub int, now int64) (int, int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.probes = 0
	if sub < 1 || sub > maxSub || now < 0 || now > maxNow {
		return 0, 0, ErrInvalidParam
	}
	if now < a.lastNow {
		return 0, 0, ErrClock
	}
	pend := a.collect(now)
	plan := a.decideOpen(sub, pend)
	if plan.err != nil {
		a.requeue(pend)
		return 0, 0, plan.err
	}
	for _, it := range pend {
		a.applyRelease(it)
	}
	a.probes += len(pend)
	a.lastNow = now
	a.log.Observe(now)
	return a.execOpen(sub, now, plan)
}

// Close returns the in-use session port (sub, addr, port) at time now.
func (a *Allocator) Close(sub, addr, port int, now int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if sub < 1 || sub > maxSub || addr < 0 || addr >= a.pool.A ||
		port < a.pool.L || port > a.pool.H || now < 0 || now > maxNow {
		return ErrInvalidParam
	}
	if now < a.lastNow {
		return ErrClock
	}
	pend := a.collect(now)
	idx := a.pool.BlockIndex(port)
	blk := &a.pool.Addrs[addr].Blocks[idx]
	if blk.Owner != sub || !blk.Get(port-a.pool.BlockFirst(idx)) {
		a.requeue(pend)
		return ErrNoSession
	}
	for _, it := range pend {
		a.applyRelease(it)
	}
	a.lastNow = now
	a.log.Observe(now)
	blk.Clear(port - a.pool.BlockFirst(idx))
	blk.Used--
	if blk.Used == 0 {
		blk.Idle = true
		blk.IdleSince = now
		heap.Push(&a.rel, relItem{at: now + a.t, idleSince: now, addr: addr, idx: idx})
	}
	return nil
}

// Drain marks an address as drained; repeated drains are accepted no-ops.
func (a *Allocator) Drain(addr int, now int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if addr < 0 || addr >= a.pool.A || now < 0 || now > maxNow {
		return ErrInvalidParam
	}
	if now < a.lastNow {
		return ErrClock
	}
	pend := a.collect(now)
	for _, it := range pend {
		a.applyRelease(it)
	}
	a.lastNow = now
	a.log.Observe(now)
	a.pool.Addrs[addr].Drained = true
	return nil
}
