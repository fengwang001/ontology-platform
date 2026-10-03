// Package seq 维护各系统计数器并负责编号签发（Issue/Reserve/Observe）。
package seq

import (
	"errors"
	"sync"

	"ontology/stride"
)

var (
	ErrParam      = errors.New("seq: 参数越界")
	ErrPermission = errors.New("seq: 权限不足")
	ErrInactive   = errors.New("seq: 系统不活跃")
	ErrState      = errors.New("seq: 状态冲突")
	ErrLast       = errors.New("seq: 最后一个活跃系统")
	ErrExhausted  = errors.New("seq: 编号耗尽")
)

const (
	MinK = 2
	MaxK = 8
	MaxM = 16

	MinN = 1
	MaxN = 1000

	MinRole   = 1
	MaxRole   = 2
	RoleAdmin = 2
)

// Store 保存全部系统的计数器、活跃标志与跳号计数 J。
// 全部方法可并发调用，效果等价于某个串行顺序。
type Store struct {
	mu      sync.Mutex
	k       int
	m       int64
	next    []int64 // 下标 1..k
	active  []bool
	jumps   int64
	touched int // 非导出计数器：最近一次 Issue/Reserve 触碰的系统数
}

// New 构造 K 系统、步长模数 m 的 Store；初始仅系统 1 活跃，next 全为 1。
func New(k int, m int64) (*Store, error) {
	if k < MinK || k > MaxK {
		return nil, ErrParam
	}
	if m < int64(k) || m > MaxM {
		return nil, ErrParam
	}
	s := &Store{
		k:      k,
		m:      m,
		next:   make([]int64, k+1),
		active: make([]bool, k+1),
	}
	for i := 1; i <= k; i++ {
		s.next[i] = 1
	}
	s.active[1] = true
	return s, nil
}

func (s *Store) K() int   { return s.k }
func (s *Store) M() int64 { return s.m }

func (s *Store) checkSys(sys int) error {
	if sys < 1 || sys > s.k {
		return ErrParam
	}
	return nil
}

func (s *Store) activeCountLocked() int {
	n := 0
	for i := 1; i <= s.k; i++ {
		if s.active[i] {
			n++
		}
	}
	return n
}

// Issue 签发一个编号，等价于 Reserve(sys, 1)。
func (s *Store) Issue(sys int) (int64, error) {
	ids, err := s.Reserve(sys, 1)
	if err != nil {
		return 0, err
	}
	return ids[0], nil
}

// Reserve 一次签发 n 个编号 next, next+stride, ...；
// 最后一个编号大于 MaxID 时整体失败且状态不变。
func (s *Store) Reserve(sys, n int) ([]int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkSys(sys); err != nil {
		return nil, err
	}
	if n < MinN || n > MaxN {
		return nil, ErrParam
	}
	if !s.active[sys] {
		return nil, ErrInactive
	}
	st := stride.Of(s.activeCountLocked(), s.m)
	base := s.next[sys]
	if last := base + int64(n-1)*st; last > stride.MaxID {
		return nil, ErrExhausted
	}
	s.touched = 1
	ids := make([]int64, n)
	for i := range ids {
		ids[i] = base + int64(i)*st
	}
	s.next[sys] = base + int64(n)*st
	return ids, nil
}

// Observe 模拟导入带原编号 id 的行：若 id >= next_sys，则把 next_sys
// 提升到 id+1（步长 1）或 alignUp(id+1, c_sys)（步长 m），增量计入 J。
func (s *Store) Observe(sys int, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkSys(sys); err != nil {
		return err
	}
	if id < 1 || id > stride.MaxID {
		return ErrParam
	}
	if !s.active[sys] {
		return ErrInactive
	}
	if id < s.next[sys] {
		return nil
	}
	var nv int64
	if st := stride.Of(s.activeCountLocked(), s.m); st == 1 {
		nv = id + 1
	} else {
		nv = stride.AlignUp(id+1, stride.Class(sys), s.m)
	}
	s.jumps += nv - s.next[sys]
	s.next[sys] = nv
	return nil
}

// NextOf 返回系统 sys 当前的 next（加锁快照）。
func (s *Store) NextOf(sys int) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.next[sys]
}

// Active 返回系统 sys 的活跃标志（加锁快照）。
func (s *Store) Active(sys int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active[sys]
}

// ActiveCount 返回当前活跃系统数（加锁快照）。
func (s *Store) ActiveCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeCountLocked()
}

// Stride 返回当前签发步长（加锁快照）。
func (s *Store) Stride() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return stride.Of(s.activeCountLocked(), s.m)
}

// J 返回跳号计数（加锁快照）。
func (s *Store) J() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.jumps
}

// setNext 是非导出的测试入口：直接设置 next_sys，不计入 J。
func (s *Store) setNext(sys int, v int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next[sys] = v
}

// View 是 Store 锁内的受限视图，供 cut 包原子地组合 Join/Leave。
type View struct {
	s *Store
}

// Locked 在 Store 锁内执行 fn；fn 返回的错误原样透传，且任何变更
// 都发生在锁内，对其它并发调用表现为一个串行点。
func (s *Store) Locked(fn func(*View) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fn(&View{s: s})
}

// ActiveCount 返回活跃系统数。
func (v *View) ActiveCount() int { return v.s.activeCountLocked() }

// Active 返回系统 sys 的活跃标志。
func (v *View) Active(sys int) bool { return v.s.active[sys] }

// Next 返回系统 sys 的 next。
func (v *View) Next(sys int) int64 { return v.s.next[sys] }

// SoleActive 当活跃数恰为 1 时返回该系统编号，否则返回 0。
func (v *View) SoleActive() int {
	sole := 0
	for i := 1; i <= v.s.k; i++ {
		if v.s.active[i] {
			if sole != 0 {
				return 0
			}
			sole = i
		}
	}
	return sole
}

// HighWater 返回全部 K 个系统（含不活跃者）next 的最大值。
func (v *View) HighWater() int64 {
	h := v.s.next[1]
	for i := 2; i <= v.s.k; i++ {
		if v.s.next[i] > h {
			h = v.s.next[i]
		}
	}
	return h
}

// SetNext 把 next_sys 置为 nv（要求 nv 不小于原值），增量计入 J。
func (v *View) SetNext(sys int, nv int64) {
	v.s.jumps += nv - v.s.next[sys]
	v.s.next[sys] = nv
}

// SetActive 设置系统 sys 的活跃标志。
func (v *View) SetActive(sys int, on bool) { v.s.active[sys] = on }
