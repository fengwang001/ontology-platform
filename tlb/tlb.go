// Package tlb implements a multi-CPU software TLB model with ASID
// generation wraparound and lazy whole-table invalidation.
//
// The model is deterministic: replaying the same operation sequence
// yields identical return values and identical observable state.
// All methods are safe for concurrent use; every operation behaves as
// if executed in some serial order, and a generation rollover is a
// single atomic step.
package tlb

import (
	"container/list"
	"errors"
	"math"
	"sync"
)

var (
	ErrBadConfig    = errors.New("tlb: invalid configuration")
	ErrTooManyMM    = errors.New("tlb: too many address spaces")
	ErrBadCPU       = errors.New("tlb: cpu index out of range")
	ErrBadMM        = errors.New("tlb: mm not created")
	ErrDead         = errors.New("tlb: mm already destroyed")
	ErrNoContext    = errors.New("tlb: cpu has no active context")
	ErrInvalidParam = errors.New("tlb: parameter out of range")
	ErrBusy         = errors.New("tlb: mm still in use by a cpu")
)

// Key identifies a single TLB entry.
type Key struct {
	ASID uint32
	VPN  uint32
}

// pair is an (asid, gen) context tag; valid reports whether it is set.
type pair struct {
	asid  uint32
	gen   uint64
	valid bool
}

type mmState struct {
	asid uint32
	gen  uint64
	dead bool
}

type entry struct {
	key Key
	pfn uint32
}

// cpuTLB is a per-CPU TLB: a fixed-capacity LRU set keyed by
// (asid, vpn), with a per-asid index so that dropping every entry of
// one asid never touches unrelated entries.
type cpuTLB struct {
	ll     *list.List // front = most recently used; values are entry
	byKey  map[uint64]*list.Element
	byASID map[uint32]map[uint32]*list.Element
}

func makeKey(asid, vpn uint32) uint64 {
	return uint64(asid)<<32 | uint64(vpn)
}

func newCPUTLB() *cpuTLB {
	return &cpuTLB{
		ll:     list.New(),
		byKey:  make(map[uint64]*list.Element),
		byASID: make(map[uint32]map[uint32]*list.Element),
	}
}

// Model is the whole software TLB model.
type Model struct {
	mu sync.Mutex

	A int // number of ASIDs; usable ASIDs are 1..A, 0 means "none"
	C int // number of CPUs
	T int // per-CPU TLB capacity
	M int // maximum number of address spaces (mm)

	G uint64 // global generation, starts at 1, never decreases

	taken    []uint64 // bitmap of taken ASIDs, bit i set <=> asid i taken
	active   []pair   // per-CPU active context
	reserved []pair   // per-CPU context reserved at the last rollover
	pending  []bool   // per-CPU lazy flush flag

	mms  []mmState
	tlbs []*cpuTLB

	// mmVisited counts iterations over the mm table performed by any
	// operation. It must always be 0: no operation may scan the mm table.
	mmVisited uint64
}

// New builds a model. A in [2,4096], C in [1,16], T in [1,64],
// M in [1,1e5], and A must be greater than C, otherwise the whole
// configuration is rejected with ErrBadConfig.
func New(a, c, t, m int) (*Model, error) {
	if a < 2 || a > 4096 || c < 1 || c > 16 || t < 1 || t > 64 ||
		m < 1 || m > 100000 || a <= c {
		return nil, ErrBadConfig
	}
	md := &Model{
		A:        a,
		C:        c,
		T:        t,
		M:        m,
		G:        1,
		taken:    make([]uint64, (a+64)/64),
		active:   make([]pair, c),
		reserved: make([]pair, c),
		pending:  make([]bool, c),
		tlbs:     make([]*cpuTLB, c),
	}
	for i := range md.tlbs {
		md.tlbs[i] = newCPUTLB()
	}
	return md, nil
}

// CreateMM creates a new address space and returns its id
// (0, 1, 2, ...). Ids are never recycled. Exceeding M fails with
// ErrTooManyMM.
func (md *Model) CreateMM() (int, error) {
	md.mu.Lock()
	defer md.mu.Unlock()
	if len(md.mms) >= md.M {
		return 0, ErrTooManyMM
	}
	md.mms = append(md.mms, mmState{})
	return len(md.mms) - 1, nil
}

// Switch moves cpu onto mm and returns the mm's (asid, gen), whether
// this cpu's TLB was flushed, and whether a rollover happened.
//
// Error order: ErrBadCPU, ErrBadMM, ErrDead.
func (md *Model) Switch(cpu, mm int) (asid uint32, gen uint64, flushed, rolled bool, err error) {
	md.mu.Lock()
	defer md.mu.Unlock()
	if cpu < 0 || cpu >= md.C {
		return 0, 0, false, false, ErrBadCPU
	}
	if mm < 0 || mm >= len(md.mms) {
		return 0, 0, false, false, ErrBadMM
	}
	if md.mms[mm].dead {
		return 0, 0, false, false, ErrDead
	}
	asid, gen, rolled = md.bind(mm)
	if md.pending[cpu] {
		md.tlbs[cpu].clear()
		md.pending[cpu] = false
		flushed = true
	}
	md.active[cpu] = pair{asid: asid, gen: gen, valid: true}
	return asid, gen, flushed, rolled, nil
}

// bind performs step 1 of Switch: decide the mm's (asid, gen) for the
// current generation, rolling over at most once.
func (md *Model) bind(mm int) (asid uint32, gen uint64, rolled bool) {
	m := &md.mms[mm]
	for {
		if m.gen == md.G {
			return m.asid, m.gen, rolled
		}
		if m.asid != 0 && md.isReserved(m.asid, m.gen) {
			m.gen = md.G // promotion: keep asid, taken unchanged
			return m.asid, m.gen, rolled
		}
		if free, ok := md.firstFree(); ok {
			m.asid, m.gen = free, md.G
			md.setTaken(free)
			return m.asid, m.gen, rolled
		}
		// Rollover: one atomic step. Because A > C and the new taken
		// set holds at most C reserved ASIDs, a free ASID always
		// exists afterwards, so this loop runs at most twice.
		md.G++
		for cpu := 0; cpu < md.C; cpu++ {
			md.reserved[cpu] = md.active[cpu]
		}
		md.taken = make([]uint64, (md.A+64)/64)
		for cpu := 0; cpu < md.C; cpu++ {
			if r := md.reserved[cpu]; r.valid {
				md.setTaken(r.asid)
			}
		}
		for cpu := 0; cpu < md.C; cpu++ {
			md.pending[cpu] = true
		}
		rolled = true
	}
}

// --- ASID bitmap helpers -------------------------------------------------

func (md *Model) setTaken(asid uint32) {
	md.taken[asid/64] |= uint64(1) << (asid % 64)
}

func (md *Model) clearTaken(asid uint32) {
	md.taken[asid/64] &^= uint64(1) << (asid % 64)
}

// firstFree returns the smallest untaken ASID in 1..A.
func (md *Model) firstFree() (uint32, bool) {
	for w := 0; w < len(md.taken); w++ {
		if md.taken[w] != math.MaxUint64 {
			for b := 0; b < 64; b++ {
				asid := uint32(w*64 + b)
				if asid < 1 || asid > uint32(md.A) {
					continue
				}
				if md.taken[w]&(uint64(1)<<uint(b)) == 0 {
					return asid, true
				}
			}
		}
	}
	return 0, false
}

// --- context helpers -----------------------------------------------------

func (md *Model) isReserved(asid uint32, gen uint64) bool {
	for cpu := 0; cpu < md.C; cpu++ {
		r := md.reserved[cpu]
		if r.valid && r.asid == asid && r.gen == gen {
			return true
		}
	}
	return false
}

func (md *Model) isReservedASID(asid uint32) bool {
	for cpu := 0; cpu < md.C; cpu++ {
		r := md.reserved[cpu]
		if r.valid && r.asid == asid {
			return true
		}
	}
	return false
}

// Fill inserts or updates (active asid, vpn) -> pfn on cpu's TLB,
// marks it most recently used, evicts the least recently used entry
// when over capacity, and reports the evicted key if any.
//
// Error order: ErrBadCPU, ErrInvalidParam, ErrNoContext.
func (md *Model) Fill(cpu int, vpn, pfn uint64) (evicted Key, ok bool, err error) {
	md.mu.Lock()
	defer md.mu.Unlock()
	if cpu < 0 || cpu >= md.C {
		return Key{}, false, ErrBadCPU
	}
	if vpn > math.MaxUint32 || pfn > math.MaxUint32 {
		return Key{}, false, ErrInvalidParam
	}
	if !md.active[cpu].valid {
		return Key{}, false, ErrNoContext
	}
	evicted, ok = md.tlbs[cpu].fill(md.active[cpu].asid, uint32(vpn), uint32(pfn), md.T)
	return evicted, ok, nil
}

// Lookup finds (active asid, vpn) on cpu's TLB. A hit returns the pfn
// and marks the entry most recently used; a miss changes nothing.
//
// Error order: ErrBadCPU, ErrInvalidParam, ErrNoContext.
func (md *Model) Lookup(cpu int, vpn uint64) (pfn uint32, hit bool, err error) {
	md.mu.Lock()
	defer md.mu.Unlock()
	if cpu < 0 || cpu >= md.C {
		return 0, false, ErrBadCPU
	}
	if vpn > math.MaxUint32 {
		return 0, false, ErrInvalidParam
	}
	if !md.active[cpu].valid {
		return 0, false, ErrNoContext
	}
	pfn, hit = md.tlbs[cpu].lookup(md.active[cpu].asid, uint32(vpn))
	return pfn, hit, nil
}

// Invalidate drops (mm.asid, vpn) from every CPU's TLB when mm's
// context is valid (current generation, or exactly matching a
// reserved pair), and returns how many CPUs lost an entry. Otherwise
// it returns 0 and changes nothing.
//
// Error order: ErrBadMM, ErrDead, ErrInvalidParam.
func (md *Model) Invalidate(mm int, vpn uint64) (int, error) {
	md.mu.Lock()
	defer md.mu.Unlock()
	if mm < 0 || mm >= len(md.mms) {
		return 0, ErrBadMM
	}
	m := &md.mms[mm]
	if m.dead {
		return 0, ErrDead
	}
	if vpn > math.MaxUint32 {
		return 0, ErrInvalidParam
	}
	if m.asid == 0 || (m.gen != md.G && !md.isReserved(m.asid, m.gen)) {
		return 0, nil
	}
	n := 0
	for cpu := 0; cpu < md.C; cpu++ {
		if md.tlbs[cpu].invalidate(m.asid, uint32(vpn)) {
			n++
		}
	}
	return n, nil
}

// DestroyMM destroys mm. Ids are not recycled. If mm's current
// context is active on some CPU it fails with ErrBusy and changes
// nothing. Otherwise mm is marked dead; when its asid belongs to the
// current generation and is not reserved by any CPU, the asid is
// released immediately: removed from taken and every TLB entry tagged
// with it is dropped. It returns whether the asid was released and
// how many TLB entries were dropped.
//
// Error order: ErrBadMM, ErrDead, ErrBusy.
func (md *Model) DestroyMM(mm int) (released bool, removed int, err error) {
	md.mu.Lock()
	defer md.mu.Unlock()
	if mm < 0 || mm >= len(md.mms) {
		return false, 0, ErrBadMM
	}
	m := &md.mms[mm]
	if m.dead {
		return false, 0, ErrDead
	}
	current := m.asid != 0 && m.gen == md.G
	if current {
		for cpu := 0; cpu < md.C; cpu++ {
			a := md.active[cpu]
			if a.valid && a.asid == m.asid && a.gen == m.gen {
				return false, 0, ErrBusy
			}
		}
	}
	m.dead = true
	if !current || md.isReservedASID(m.asid) {
		return false, 0, nil
	}
	md.clearTaken(m.asid)
	for cpu := 0; cpu < md.C; cpu++ {
		removed += md.tlbs[cpu].dropASID(m.asid)
	}
	return true, removed, nil
}

// --- per-CPU TLB primitives ----------------------------------------------

func (t *cpuTLB) clear() {
	t.ll.Init()
	t.byKey = make(map[uint64]*list.Element)
	t.byASID = make(map[uint32]map[uint32]*list.Element)
}

func (t *cpuTLB) remove(e *list.Element) {
	en := e.Value.(entry)
	t.ll.Remove(e)
	delete(t.byKey, makeKey(en.key.ASID, en.key.VPN))
	vpns := t.byASID[en.key.ASID]
	delete(vpns, en.key.VPN)
	if len(vpns) == 0 {
		delete(t.byASID, en.key.ASID)
	}
}

func (t *cpuTLB) fill(asid, vpn, pfn uint32, capacity int) (Key, bool) {
	k := makeKey(asid, vpn)
	if e, ok := t.byKey[k]; ok {
		e.Value = entry{key: Key{ASID: asid, VPN: vpn}, pfn: pfn}
		t.ll.MoveToFront(e)
		return Key{}, false
	}
	e := t.ll.PushFront(entry{key: Key{ASID: asid, VPN: vpn}, pfn: pfn})
	t.byKey[k] = e
	vpns := t.byASID[asid]
	if vpns == nil {
		vpns = make(map[uint32]*list.Element)
		t.byASID[asid] = vpns
	}
	vpns[vpn] = e
	if t.ll.Len() > capacity {
		back := t.ll.Back()
		en := back.Value.(entry)
		t.remove(back)
		return en.key, true
	}
	return Key{}, false
}

func (t *cpuTLB) lookup(asid, vpn uint32) (uint32, bool) {
	e, ok := t.byKey[makeKey(asid, vpn)]
	if !ok {
		return 0, false
	}
	t.ll.MoveToFront(e)
	return e.Value.(entry).pfn, true
}

func (t *cpuTLB) invalidate(asid, vpn uint32) bool {
	e, ok := t.byKey[makeKey(asid, vpn)]
	if !ok {
		return false
	}
	t.remove(e)
	return true
}

func (t *cpuTLB) dropASID(asid uint32) int {
	vpns := t.byASID[asid]
	if len(vpns) == 0 {
		return 0
	}
	n := len(vpns)
	for _, e := range vpns {
		en := e.Value.(entry)
		t.ll.Remove(e)
		delete(t.byKey, makeKey(en.key.ASID, en.key.VPN))
	}
	delete(t.byASID, asid)
	return n
}

// keys returns the TLB contents, most recently used first.
// It is used by tests and diagnostics.
func (t *cpuTLB) keys() []Key {
	out := make([]Key, 0, t.ll.Len())
	for e := t.ll.Front(); e != nil; e = e.Next() {
		out = append(out, e.Value.(entry).key)
	}
	return out
}
