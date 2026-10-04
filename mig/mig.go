// Package mig 实现键编码在线迁移的混合有序存储：
// 水位路由、按逻辑升序搬迁、崩溃恢复与混合扫描。
//
// 不变式（崩溃残留除外）：A（旧编码 E1）中逻辑键均不小于水位 w，
// B（新编码 E2）中逻辑键均小于 w。读写按 k<w 路由 B、否则路由 A；
// Scan 取 B 的 [lo, min(hi,w)) 与 A 的 [max(lo,w), hi) 两段拼接。
// 崩溃恢复以水位推进为提交点：p=1 回滚（删 B 中 >=w 的残留），
// p=2 前滚（删 A 中 <w 的残留）。
//
// 所有方法可并发调用，内部以单互斥锁串行化，结果等价于某个串行顺序。
package mig

import (
	"errors"
	"sync"

	"ontology/keyenc"
	"ontology/store"
)

// 逻辑键域为 [MinKey, MaxKey]；水位与 Scan 上界可达 MaxKey+1。
const (
	MinKey int64 = -1_000_000_000_000
	MaxKey int64 = 1_000_000_000_000
)

var (
	ErrInvalid    = errors.New("mig: invalid argument")
	ErrCrashed    = errors.New("mig: crashed")
	ErrNotCrashed = errors.New("mig: not crashed")
	ErrDrained    = errors.New("mig: A is drained")
	ErrNotDrained = errors.New("mig: A is not drained")
)

// Entry 为 Scan 返回的逻辑键值对。
type Entry struct {
	Key int64
	Val int64
}

// Migrator 为混合有序存储。零值不可用，须用 New 构造。
type Migrator struct {
	mu       sync.Mutex
	a        *store.Store // 旧编码 E1
	b        *store.Store // 新编码 E2
	w        int64        // 水位（逻辑键）
	crashed  bool
	scanPhys int // 非导出计数器：Scan 的物理产出总条数
	probes   int // 非导出计数器：最近一次 Step/StepPartial 探测 A 的条目数
}

// New 返回全新实例，水位为 MinKey，A、B 均为空。
func New() *Migrator {
	return &Migrator{a: store.New(), b: store.New(), w: MinKey}
}

func validKey(k int64) bool { return k >= MinKey && k <= MaxKey }

// Put 写入（覆盖）键值；k<w 走 B，否则走 A。
func (m *Migrator) Put(k, v int64) error {
	if !validKey(k) {
		return ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.crashed {
		return ErrCrashed
	}
	if k < m.w {
		e := keyenc.E2(k)
		m.b.Put(e[:], v)
	} else {
		e := keyenc.E1(k)
		m.a.Put(e[:], v)
	}
	return nil
}

// Get 点读，返回 (值, 是否存在)；崩溃态下仍按水位规则工作。
func (m *Migrator) Get(k int64) (int64, bool, error) {
	if !validKey(k) {
		return 0, false, ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if k < m.w {
		e := keyenc.E2(k)
		v, ok := m.b.Get(e[:])
		return v, ok, nil
	}
	e := keyenc.E1(k)
	v, ok := m.a.Get(e[:])
	return v, ok, nil
}

// Delete 点删，返回键是否存在。
func (m *Migrator) Delete(k int64) (bool, error) {
	if !validKey(k) {
		return false, ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.crashed {
		return false, ErrCrashed
	}
	if k < m.w {
		e := keyenc.E2(k)
		return m.b.Delete(e[:]), nil
	}
	e := keyenc.E1(k)
	return m.a.Delete(e[:]), nil
}

// Scan 返回逻辑键在 [lo, hi) 内的全部条目，按逻辑键升序；
// 崩溃态下仍按水位规则工作（残留不可见）。
func (m *Migrator) Scan(lo, hi int64) ([]Entry, error) {
	if lo < MinKey || hi > MaxKey+1 || lo > hi {
		return nil, ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Entry
	if bHi := min(hi, m.w); lo < bHi {
		for _, iv := range keyenc.Range2(lo, bHi) {
			for _, e := range m.b.Scan(iv.Lo, iv.Hi, 0) {
				m.scanPhys++
				out = append(out, Entry{Key: keyenc.Decode2(e.Key), Val: e.Val})
			}
		}
	}
	if aLo := max(lo, m.w); aLo < hi {
		for _, iv := range keyenc.Range1(aLo, hi) {
			for _, e := range m.a.Scan(iv.Lo, iv.Hi, 0) {
				m.scanPhys++
				out = append(out, Entry{Key: keyenc.Decode1(e.Key), Val: e.Val})
			}
		}
	}
	return out, nil
}

// logicalMinA 返回 A 中逻辑最小的键值对。
// 至多探测 A 两条目：最小负数候选（[E1(MinKey), +inf) 首条）与物理首条
// （无负数时即最小非负数），与 A 的规模无关。
func (m *Migrator) logicalMinA() (int64, int64, bool) {
	loB := keyenc.E1(MinKey)
	if ents := m.a.Scan(loB[:], nil, 1); len(ents) > 0 {
		m.probes++
		return keyenc.Decode1(ents[0].Key), ents[0].Val, true
	}
	if ents := m.a.Scan(nil, nil, 1); len(ents) > 0 {
		m.probes++
		return keyenc.Decode1(ents[0].Key), ents[0].Val, true
	}
	return 0, 0, false
}

// Step 按逻辑升序把 A 中最小的至多 n 个键搬迁到 B，返回实际搬迁数；
// A 为空返回 0。每个键依次：写 B、w 置为 k+1、从 A 删除。
func (m *Migrator) Step(n int) (int, error) {
	if n < 1 || n > 10_000 {
		return 0, ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.crashed {
		return 0, ErrCrashed
	}
	m.probes = 0
	moved := 0
	for moved < n {
		k, v, ok := m.logicalMinA()
		if !ok {
			break
		}
		e2 := keyenc.E2(k)
		m.b.Put(e2[:], v)
		m.w = k + 1
		e1 := keyenc.E1(k)
		m.a.Delete(e1[:])
		moved++
	}
	return moved, nil
}

// StepPartial 只对 A 中逻辑最小的键执行搬迁的前 p 步，随后进入崩溃态；
// A 为空报 ErrDrained。
func (m *Migrator) StepPartial(p int) error {
	if p != 1 && p != 2 {
		return ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.crashed {
		return ErrCrashed
	}
	k, v, ok := m.logicalMinA()
	if !ok {
		return ErrDrained
	}
	e2 := keyenc.E2(k)
	m.b.Put(e2[:], v) // 子步骤 1：写入 B
	if p == 2 {
		m.w = k + 1 // 子步骤 2：水位推进（提交点）
	}
	m.crashed = true
	return nil
}

// Recover 删除 B 中逻辑键不小于 w 的条目与 A 中逻辑键小于 w 的条目，
// 返回 (B 删除数, A 删除数)，并恢复正常态；非崩溃态报 ErrNotCrashed。
func (m *Migrator) Recover() (int, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.crashed {
		return 0, 0, ErrNotCrashed
	}
	loB := keyenc.E2(m.w)
	bDel := deleteRange(m.b, loB[:], nil)
	aDel := 0
	for _, iv := range keyenc.Range1(MinKey, m.w) {
		aDel += deleteRange(m.a, iv.Lo, iv.Hi)
	}
	m.crashed = false
	return bDel, aDel, nil
}

func deleteRange(s *store.Store, lo, hi []byte) int {
	ents := s.Scan(lo, hi, 0)
	for _, e := range ents {
		s.Delete(e.Key)
	}
	return len(ents)
}

// Finish 要求 A 为空（否则 ErrNotDrained）；成功后 w 置为 MaxKey+1，
// 此后一切读写都走 B。
func (m *Migrator) Finish() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.crashed {
		return ErrCrashed
	}
	if m.a.Len() > 0 {
		return ErrNotDrained
	}
	m.w = MaxKey + 1
	return nil
}
