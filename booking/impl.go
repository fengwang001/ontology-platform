package booking

import (
	"math/bits"
	"sort"
	"sync"
)

type entry struct {
	id   string
	mask uint32
	qty  int64
}

type bookState struct {
	c      int
	rho    int64
	stocks []int64

	capacity []int64 // 按掩码索引，cap 长度为 1<<c

	// supersets[t] 为所有包含 t 的非空子集（含 t 自身）。
	supersets [][]uint32

	booked  map[string]*entry
	waiting []*entry // 候补队列，严格保持到达顺序

	mu sync.Mutex

	counterSubsets int64
	counterCalls   int64
}

// New 创建预订簿。
//
// c 取 1..10；每个 stock 取 0..1e12；rho 为百分数，取 1..1000。
func New(c int, stocks []int64, rho int) (*Book, error) {
	if c < 1 || c > 10 {
		return nil, ErrInvalid
	}
	if len(stocks) != c {
		return nil, ErrInvalid
	}
	for _, s := range stocks {
		if s < 0 || s > 1_000_000_000_000 {
			return nil, ErrInvalid
		}
	}
	if rho < 1 || rho > 1000 {
		return nil, ErrInvalid
	}

	size := 1 << c
	st := &bookState{
		c:         c,
		rho:       int64(rho),
		stocks:    append([]int64(nil), stocks...),
		capacity:  make([]int64, size),
		supersets: make([][]uint32, size),
		booked:    make(map[string]*entry),
	}
	for t := 1; t < size; t++ {
		var sum int64
		for j := 0; j < c; j++ {
			if uint32(t)&(1<<uint(j)) != 0 {
				sum += stocks[j]
			}
		}
		// 整体求和后再取整：Cap(T) = floor(rho * S(T) / 100)。
		// 用 128 位乘法避免 rho*S(T) 溢出（最大 1000*10*1e12≈1e16）。
		hi, lo := bits.Mul64(uint64(st.rho), uint64(sum))
		quot, _ := bits.Div64(hi, lo, 100)
		st.capacity[t] = int64(quot)
	}
	for t := 1; t < size; t++ {
		full := uint32(size - 1)
		m := uint32(t)
		rest := full ^ m
		list := make([]uint32, 0, 1<<uint(c-popcount(m)))
		// 枚举 rest 的所有子集 sub，目标集合为 sub|m，天然非空。
		sub := uint32(0)
		for {
			list = append(list, sub|m)
			if sub == rest {
				break
			}
			sub = (sub - rest) & rest
		}
		st.supersets[t] = list
	}
	return &Book{st: st}, nil
}

func popcount(m uint32) int {
	n := 0
	for m != 0 {
		n += int(m & 1)
		m >>= 1
	}
	return n
}

func (s *bookState) validMask(mask uint32) bool {
	return mask != 0 && mask < uint32(1<<uint(s.c))
}

func (s *bookState) validQty(qty int64) bool {
	return qty >= 1 && qty <= 1_000_000_000_000
}

// feasibleChange 判断：在当前已预订集合上叠加变更 delta
// （e.qty>0 表示新增/加量，<0 表示减量）后，枚举给定的全部子集是否可行。
// 必须枚举全部子集，不提前退出；返回枚举的子集数与最终结论。
func (s *bookState) feasibleChange(e *entry, delta int64, subsets []uint32) (bool, int) {
	n := len(subsets)
	ok := true
	for _, t := range subsets {
		var load int64
		for _, x := range s.booked {
			if x.mask&t == x.mask {
				load += x.qty
			}
		}
		if e != nil && e.mask&t == e.mask {
			load += delta
		}
		if load > s.capacity[t] {
			ok = false
		}
	}
	return ok, n
}

// wouldFitLocked 供包内测试在加锁前提下判定候补项是否可加入。
func (s *bookState) wouldFitLocked(mask uint32, qty int64) bool {
	e := &entry{mask: mask, qty: qty}
	ok, _ := s.feasibleChange(e, qty, s.supersets[mask])
	return ok
}

// consistentLocked 在单次持锁内验证：已预订集合可行，且每个候补项加入都不可行。
func (s *bookState) consistentLocked() bool {
	for t := 1; t < len(s.capacity); t++ {
		var load int64
		for _, x := range s.booked {
			if x.mask&uint32(t) == x.mask {
				load += x.qty
			}
		}
		if load > s.capacity[t] {
			return false
		}
	}
	for _, w := range s.waiting {
		ok, _ := s.feasibleChange(w, w.qty, s.supersets[w.mask])
		if ok {
			return false
		}
	}
	return true
}

// promote 执行一轮候补递补扫描：按到达顺序只扫一遍，可行即转已预订，
// 不可行留在原位并继续检查后续项。返回本轮递补的 id 顺序与枚举子集总数。
func (s *bookState) promote() ([]string, int) {
	promoted := []string{}
	checked := 0
	kept := s.waiting[:0]
	for _, e := range s.waiting {
		ok, n := s.feasibleChange(e, e.qty, s.supersets[e.mask])
		checked += n
		s.counterCalls++
		s.counterSubsets += int64(n)
		if ok {
			s.booked[e.id] = e
			promoted = append(promoted, e.id)
		} else {
			kept = append(kept, e)
		}
	}
	s.waiting = kept
	return promoted, checked
}

func (s *bookState) snapshot(promoted []string, checked int, ok bool, reason error, phase Phase) Result {
	booked := make([]Contract, 0, len(s.booked))
	for _, e := range s.booked {
		booked = append(booked, Contract{ID: e.id, Mask: e.mask, Qty: e.qty})
	}
	sort.Slice(booked, func(i, j int) bool { return booked[i].ID < booked[j].ID })
	waiting := make([]Contract, 0, len(s.waiting))
	for _, e := range s.waiting {
		waiting = append(waiting, Contract{ID: e.id, Mask: e.mask, Qty: e.qty})
	}
	return Result{
		OK:             ok,
		Reason:         reason,
		Phase:          phase,
		SubsetsChecked: checked,
		Promoted:       promoted,
		Booked:         booked,
		Waiting:        waiting,
	}
}

func (s *bookState) doBook(id string, mask uint32, qty int64) Result {
	s.mu.Lock()
	defer s.mu.Unlock()

	if id == "" || !s.validMask(mask) || !s.validQty(qty) {
		return s.snapshot(nil, 0, false, ErrInvalid, 0)
	}
	if _, exists := s.booked[id]; exists {
		return s.snapshot(nil, 0, false, ErrAlreadyExists, PhaseBooked)
	}
	for _, e := range s.waiting {
		if e.id == id {
			return s.snapshot(nil, 0, false, ErrAlreadyExists, PhaseWaiting)
		}
	}

	e := &entry{id: id, mask: mask, qty: qty}

	// 单点判定：单合约独占是否永不可订。该判定不计入子集计数器。
	if qty > s.capacity[mask] {
		return s.snapshot(nil, 0, false, ErrNeverBookable, 0)
	}

	ok, n := s.feasibleChange(e, qty, s.supersets[mask])
	s.counterCalls++
	s.counterSubsets += int64(n)
	if ok {
		s.booked[id] = e
		return s.snapshot(nil, n, true, nil, PhaseBooked)
	}
	s.waiting = append(s.waiting, e)
	return s.snapshot(nil, n, false, ErrInfeasible, PhaseWaiting)
}

func (s *bookState) doCancel(id string) Result {
	s.mu.Lock()
	defer s.mu.Unlock()

	if id == "" {
		return s.snapshot(nil, 0, false, ErrInvalid, 0)
	}
	if e, ok := s.booked[id]; ok {
		delete(s.booked, id)
		promoted, checked := s.promote()
		_ = e
		return s.snapshot(promoted, checked, true, nil, 0)
	}
	for i, e := range s.waiting {
		if e.id == id {
			s.waiting = append(s.waiting[:i], s.waiting[i+1:]...)
			// 取消候补项不触发递补。
			return s.snapshot(nil, 0, true, nil, PhaseWaiting)
		}
	}
	return s.snapshot(nil, 0, false, ErrNotFound, 0)
}

func (s *bookState) doResize(id string, q2 int64) Result {
	s.mu.Lock()
	defer s.mu.Unlock()

	if id == "" || !s.validQty(q2) {
		return s.snapshot(nil, 0, false, ErrInvalid, 0)
	}
	e, ok := s.booked[id]
	if !ok {
		if s.waitingIndex(id) >= 0 {
			return s.snapshot(nil, 0, false, ErrNotBooked, PhaseWaiting)
		}
		return s.snapshot(nil, 0, false, ErrNotFound, 0)
	}
	if q2 == e.qty {
		return s.snapshot(nil, 0, true, nil, PhaseBooked)
	}

	if q2 < e.qty {
		// 变小必然可行：直接改量并触发递补。
		e.qty = q2
		promoted, checked := s.promote()
		return s.snapshot(promoted, checked, true, nil, PhaseBooked)
	}

	ok2, n := s.feasibleChange(e, q2-e.qty, s.supersets[e.mask])
	s.counterCalls++
	s.counterSubsets += int64(n)
	if !ok2 {
		return s.snapshot(nil, n, false, ErrInfeasible, PhaseBooked)
	}
	e.qty = q2
	return s.snapshot(nil, n, true, nil, PhaseBooked)
}

func (s *bookState) waitingIndex(id string) int {
	for i, e := range s.waiting {
		if e.id == id {
			return i
		}
	}
	return -1
}

func (s *bookState) doRetarget(id string, mask2 uint32) Result {
	s.mu.Lock()
	defer s.mu.Unlock()

	if id == "" || !s.validMask(mask2) {
		return s.snapshot(nil, 0, false, ErrInvalid, 0)
	}
	e, ok := s.booked[id]
	if !ok {
		if s.waitingIndex(id) >= 0 {
			return s.snapshot(nil, 0, false, ErrNotBooked, PhaseWaiting)
		}
		return s.snapshot(nil, 0, false, ErrNotFound, 0)
	}
	if mask2 == e.mask {
		return s.snapshot(nil, 0, true, nil, PhaseBooked)
	}

	// 只检查 T 包含 mask2 而不包含旧 mask 的子集。
	targets := make([]uint32, 0)
	ok2 := true
	for _, t := range s.supersets[mask2] {
		if t&e.mask == e.mask {
			continue // T 也包含旧 mask：该约束在重定向前已满足
		}
		var load int64
		for _, x := range s.booked {
			if x.id == id {
				continue
			}
			if x.mask&t == x.mask {
				load += x.qty
			}
		}
		load += e.qty
		if load > s.capacity[t] {
			ok2 = false
		}
		targets = append(targets, t)
	}
	n := len(targets)
	if n > 0 {
		s.counterCalls++
		s.counterSubsets += int64(n)
	}
	if !ok2 {
		return s.snapshot(nil, n, false, ErrInfeasible, PhaseBooked)
	}
	e.mask = mask2
	promoted, checked := s.promote()
	return s.snapshot(promoted, n+checked, true, nil, PhaseBooked)
}
