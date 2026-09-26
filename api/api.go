// Package api 是布谷鸟哈希的对外门面。依赖 cuckoo。
package api

import (
	"errors"
	"fmt"

	"ontology/cuckoo"
)

// 对外可判定的哨兵错误，四者互不相同。
var (
	ErrExists       = cuckoo.ErrExists
	ErrNotFound     = cuckoo.ErrNotFound
	ErrTableFull    = cuckoo.ErrTableFull
	ErrInvalidParam = errors.New("api: n must be >= 1")
)

// Handle 是一个并发安全的布谷鸟哈希实例。
type Handle struct{ t *cuckoo.Table }

// New 构造两张各 n 槽、驱逐上限 maxKicks 的表；n < 1 返回 ErrInvalidParam。
func New(n, maxKicks int) (*Handle, error) {
	if n < 1 {
		return nil, ErrInvalidParam
	}
	return &Handle{t: cuckoo.NewTable(n, maxKicks)}, nil
}

// Insert 插入键；重复返回 ErrExists，驱逐超限返回 ErrTableFull（表不变）。
func (h *Handle) Insert(x int) error { return h.t.Insert(x) }

// Lookup 查找键；不存在返回 ErrNotFound。
func (h *Handle) Lookup(x int) (bool, error) {
	if h.t.Lookup(x) {
		return true, nil
	}
	return false, ErrNotFound
}

// Delete 删除键；不存在返回 ErrNotFound。
func (h *Handle) Delete(x int) error { return h.t.Delete(x) }

// Len 返回当前键数。
func (h *Handle) Len() int { return h.t.Len() }

// Snapshot 返回两张表的副本，供演示核对（空槽 Occupied=false）。
func (h *Handle) Snapshot() ([]cuckoo.Slot, []cuckoo.Slot) { return h.t.Snapshot() }

var selfKeys = []int{0, 1, 2, 3, 4, 5, 6, 7, 100, -3, 42, 999}

// SelfCheck 用内置键序列核验四条不变量；全部通过返回 nil。
// 只操作内部新建的实例，不改 h 的状态，可并发调用。
func (h *Handle) SelfCheck() error {
	// 不变量 1：Insert 后 Lookup 必真，Delete 后必假。
	a, _ := New(64, 64)
	for _, x := range selfKeys {
		if err := a.Insert(x); err != nil {
			return fmt.Errorf("selfcheck inv1 insert: %w", err)
		}
	}
	for _, x := range selfKeys {
		if ok, err := a.Lookup(x); !ok || err != nil {
			return fmt.Errorf("selfcheck inv1 lookup %d", x)
		}
	}
	for _, x := range selfKeys[:4] {
		if err := a.Delete(x); err != nil {
			return fmt.Errorf("selfcheck inv1 delete: %w", err)
		}
		if ok, _ := a.Lookup(x); ok {
			return fmt.Errorf("selfcheck inv1: %d still present after delete", x)
		}
	}
	// 不变量 2：与朴素 map 参照逐键一致。
	b, _ := New(64, 64)
	ref := map[int]bool{}
	for i, x := range selfKeys {
		if i%2 == 0 {
			_ = b.Insert(x)
			ref[x] = true
		}
	}
	for _, x := range selfKeys {
		if ok, _ := b.Lookup(x); ok != ref[x] {
			return fmt.Errorf("selfcheck inv2: lookup %d mismatch", x)
		}
	}
	// 不变量 3：n=4 驱逐成环，ErrTableFull 且两表原样不动。
	c, _ := New(4, 16)
	for x := 0; x < 8; x++ {
		_ = c.Insert(x)
	}
	b1, b2 := c.Snapshot()
	if err := c.Insert(8); !errors.Is(err, ErrTableFull) {
		return fmt.Errorf("selfcheck inv3: want ErrTableFull, got %v", err)
	}
	a1, a2 := c.Snapshot()
	if !slotsEq(b1, a1) || !slotsEq(b2, a2) {
		return errors.New("selfcheck inv3: tables changed after ErrTableFull")
	}
	// 不变量 4：四类错误互不相同，被拒后状态不变、可继续用。
	if _, err := New(0, 1); !errors.Is(err, ErrInvalidParam) {
		return fmt.Errorf("selfcheck inv4: want ErrInvalidParam, got %v", err)
	}
	sent := []error{ErrExists, ErrNotFound, ErrTableFull, ErrInvalidParam}
	for i := range sent {
		for j := range sent {
			if (i == j) != errors.Is(sent[i], sent[j]) {
				return errors.New("selfcheck inv4: sentinel errors not distinct")
			}
		}
	}
	d, _ := New(8, 8)
	_ = d.Insert(7)
	n0 := d.Len()
	s1, s2 := d.Snapshot()
	if err := d.Insert(7); !errors.Is(err, ErrExists) {
		return fmt.Errorf("selfcheck inv4: want ErrExists, got %v", err)
	}
	if _, err := d.Lookup(9); !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("selfcheck inv4: want ErrNotFound, got %v", err)
	}
	if err := d.Delete(9); !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("selfcheck inv4: want ErrNotFound, got %v", err)
	}
	r1, r2 := d.Snapshot()
	if d.Len() != n0 || !slotsEq(s1, r1) || !slotsEq(s2, r2) {
		return errors.New("selfcheck inv4: state changed after rejected ops")
	}
	if err := d.Insert(8); err != nil {
		return fmt.Errorf("selfcheck inv4: unusable after rejects: %w", err)
	}
	return nil
}

func slotsEq(a, b []cuckoo.Slot) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
