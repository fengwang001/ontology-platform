package tlb

import (
	"math"
)

// 本文件是按题目规则逐条写成的朴素模拟，用于与优化实现对照。
// 它刻意使用逐 mm 遍历与逐条目线性查找，不作任何性能优化，
// 以便让"正确性"与"性能"两份代码相互独立。

type naivePair struct {
	asid uint32
	gen  uint64
}

type naiveKey struct {
	asid uint32
	vpn  uint32
}

type naiveEntry struct {
	key naiveKey
	pfn uint32
}

type naiveMM struct {
	asid  uint32
	gen   uint64
	alive bool
}

type naiveCPU struct {
	active   naivePair
	reserved naivePair
	pending  bool
	tlb      []naiveEntry // 最近使用在前
}

type naive struct {
	A, C, T, M int
	G          uint64
	taken      map[uint32]bool
	cpus       []naiveCPU
	mms        []naiveMM
}

func newNaive(cfg Config) *naive {
	return &naive{
		A: cfg.ASIDs, C: cfg.CPUs, T: cfg.TLBCap, M: cfg.MaxMMs,
		G:     1,
		taken: make(map[uint32]bool),
		cpus:  make([]naiveCPU, cfg.CPUs),
	}
}

func (n *naive) reservedHas(asid uint32, gen uint64) bool {
	for i := range n.cpus {
		if r := n.cpus[i].reserved; r.asid == asid && r.gen == gen {
			return true
		}
	}
	return false
}

func (n *naive) reservedASID(asid uint32) bool {
	for i := range n.cpus {
		if n.cpus[i].reserved.asid == asid {
			return true
		}
	}
	return false
}

func (n *naive) createMM() (int, error) {
	if len(n.mms) >= n.M {
		return 0, ErrTooManyMM
	}
	n.mms = append(n.mms, naiveMM{alive: true})
	return len(n.mms) - 1, nil
}

func (n *naive) switchTo(cpu, mm int) (uint32, uint64, bool, bool, error) {
	if cpu < 0 || cpu >= n.C {
		return 0, 0, false, false, ErrBadCPU
	}
	if mm < 0 || mm >= len(n.mms) {
		return 0, 0, false, false, ErrBadMM
	}
	e := &n.mms[mm]
	if !e.alive {
		return 0, 0, false, false, ErrDead
	}
	rolled := false
	for attempt := 0; attempt < 2; attempt++ {
		if e.gen == n.G {
			break
		}
		if e.asid != 0 && n.reservedHas(e.asid, e.gen) {
			e.gen = n.G
			break
		}
		free := uint32(0)
		for a := uint32(1); int(a) <= n.A; a++ { // 逐编号线性扫描
			if !n.taken[a] {
				free = a
				break
			}
		}
		if free != 0 {
			n.taken[free] = true
			e.asid, e.gen = free, n.G
			break
		}
		// 回绕
		n.G++
		for i := range n.cpus {
			n.cpus[i].reserved = n.cpus[i].active
			n.cpus[i].pending = true
		}
		n.taken = make(map[uint32]bool)
		for i := range n.cpus {
			if r := n.cpus[i].reserved; r.asid != 0 {
				n.taken[r.asid] = true
			}
		}
		rolled = true
	}
	flushed := false
	c := &n.cpus[cpu]
	if c.pending {
		c.tlb = nil
		c.pending = false
		flushed = true
	}
	c.active = naivePair{e.asid, e.gen}
	return e.asid, e.gen, flushed, rolled, nil
}

func (n *naive) fill(cpu int, vpn, pfn uint64) (Key, bool, error) {
	if cpu < 0 || cpu >= n.C {
		return Key{}, false, ErrBadCPU
	}
	if vpn > math.MaxUint32 || pfn > math.MaxUint32 {
		return Key{}, false, ErrBadParam
	}
	c := &n.cpus[cpu]
	if c.active.asid == 0 {
		return Key{}, false, ErrNoContext
	}
	key := naiveKey{c.active.asid, uint32(vpn)}
	for i, e := range c.tlb { // 线性查找
		if e.key == key {
			c.tlb[i].pfn = uint32(pfn)
			moveToFront(c, i)
			return Key{}, false, nil
		}
	}
	c.tlb = append([]naiveEntry{{key, uint32(pfn)}}, c.tlb...)
	if len(c.tlb) > n.T {
		evicted := c.tlb[len(c.tlb)-1]
		c.tlb = c.tlb[:len(c.tlb)-1]
		return Key{evicted.key.asid, evicted.key.vpn}, true, nil
	}
	return Key{}, false, nil
}

func (n *naive) lookup(cpu int, vpn uint64) (uint32, bool, error) {
	if cpu < 0 || cpu >= n.C {
		return 0, false, ErrBadCPU
	}
	if vpn > math.MaxUint32 {
		return 0, false, ErrBadParam
	}
	c := &n.cpus[cpu]
	if c.active.asid == 0 {
		return 0, false, ErrNoContext
	}
	key := naiveKey{c.active.asid, uint32(vpn)}
	for i, e := range c.tlb { // 线性查找
		if e.key == key {
			pfn := e.pfn
			moveToFront(c, i)
			return pfn, true, nil
		}
	}
	return 0, false, nil
}

// moveToFront 把第 i 条移到最前（最近使用）。
func moveToFront(c *naiveCPU, i int) {
	e := c.tlb[i]
	copy(c.tlb[1:i+1], c.tlb[0:i])
	c.tlb[0] = e
}

func (n *naive) invalidate(mm int, vpn uint64) (int, error) {
	if mm < 0 || mm >= len(n.mms) {
		return 0, ErrBadMM
	}
	e := &n.mms[mm]
	if !e.alive {
		return 0, ErrDead
	}
	if vpn > math.MaxUint32 {
		return 0, ErrBadParam
	}
	if e.asid == 0 || (e.gen != n.G && !n.reservedHas(e.asid, e.gen)) {
		return 0, nil
	}
	removed := 0
	key := naiveKey{e.asid, uint32(vpn)}
	for i := range n.cpus {
		c := &n.cpus[i]
		for j, ent := range c.tlb { // 线性查找
			if ent.key == key {
				c.tlb = append(c.tlb[:j], c.tlb[j+1:]...)
				removed++
				break
			}
		}
	}
	return removed, nil
}

func (n *naive) destroyMM(mm int) (bool, int, error) {
	if mm < 0 || mm >= len(n.mms) {
		return false, 0, ErrBadMM
	}
	e := &n.mms[mm]
	if !e.alive {
		return false, 0, ErrDead
	}
	if e.asid != 0 && e.gen == n.G {
		for i := range n.cpus {
			if a := n.cpus[i].active; a.asid == e.asid && a.gen == e.gen {
				return false, 0, ErrBusy
			}
		}
	}
	e.alive = false
	if e.asid != 0 && e.gen == n.G && !n.reservedASID(e.asid) {
		delete(n.taken, e.asid)
		removed := 0
		for i := range n.cpus {
			c := &n.cpus[i]
			kept := c.tlb[:0]
			for _, ent := range c.tlb { // 逐条目线性扫描
				if ent.key.asid == e.asid {
					removed++
				} else {
					kept = append(kept, ent)
				}
			}
			c.tlb = kept
		}
		return true, removed, nil
	}
	return false, 0, nil
}
