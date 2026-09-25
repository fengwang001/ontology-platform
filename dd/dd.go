// Package dd 维护 rowID→(key,元组) 行索引，写时对称撤回，按引用计数 0↔1 翻转增量维护 distinct；依赖 tup。
package dd

import (
	"errors"
	"fmt"
	"sync"

	"ontology/tup"
)

// 三类可判定、互不相同的哨兵错误。
var (
	ErrRowNotFound  = errors.New("dd: row not found")          // Delete 的 rowID 不存在
	ErrEmptyKey     = errors.New("dd: key must not be empty")  // Upsert key 为空串
	ErrInvalidRowID = errors.New("dd: rowID must be positive") // rowID 非正
)

type cur struct {
	key string
	t   tup.T
}

// Engine 是行索引与增量 distinct 计数的全部状态，进程内存、仅用标准库。
type Engine struct {
	mu     sync.RWMutex
	cat    *tup.Catalog // 分组 → 元组 → 引用计数（0↔1 翻转时维护分组 distinct）
	rows   map[int]cur  // rowID → 当前所在分组与元组
	checks int          // 非导出：最近一次写操作检查的元组个数，仅供同包测试读取
}

// NewEngine 创建空引擎。
func NewEngine() *Engine {
	return &Engine{cat: tup.NewCatalog(), rows: make(map[int]cur)}
}

// adjust 调整一个元组的引用计数（±1），并把"检查过的元组个数"记 1。
func (e *Engine) adjust(key string, t tup.T, delta int) {
	e.cat.Adjust(key, t, delta)
	e.checks++
}

// Upsert 新增或更新一行：已存在时先在旧 key 下撤回旧元组（恰减 1）、再为新
// 元组加 1；校验在改写前完成，被拒不留痕。
func (e *Engine) Upsert(rowID int, key, col1 string, col2 int) error {
	if rowID <= 0 {
		return ErrInvalidRowID
	}
	if key == "" {
		return ErrEmptyKey
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.checks = 0
	t := tup.T{C1: col1, C2: col2}
	if old, ok := e.rows[rowID]; ok {
		e.adjust(old.key, old.t, -1)
	}
	e.adjust(key, t, +1)
	e.rows[rowID] = cur{key: key, t: t}
	return nil
}

// Delete 撤回 rowID 当前元组（恰减 1）并移除该行；不存在时整体失败、状态不变。
func (e *Engine) Delete(rowID int) error {
	if rowID <= 0 {
		return ErrInvalidRowID
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	old, ok := e.rows[rowID]
	if !ok {
		return ErrRowNotFound
	}
	e.checks = 0
	e.adjust(old.key, old.t, -1)
	delete(e.rows, rowID)
	return nil
}

// Distinct 返回 key 分组活跃行按 (Col1,Col2) 去重后的元组个数。
func (e *Engine) Distinct(key string) int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cat.Distinct(key)
}

// Total 返回所有分组 distinct 计数之和。
func (e *Engine) Total() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cat.Total()
}

// SelfCheck 在独立引擎上跑内置序列核验第二节四条不变量，只给 pass/fail。
func (e *Engine) SelfCheck() error {
	p := NewEngine()
	want := [...]int{1, 2, 3, 3, 4, 3, 2, 2}   // 八步后 Distinct("k")
	refA10 := [...]int{1, 1, 1, 2, 1, 0, 0, 0} // 各步后 (a,10) 引用计数
	del := [...]bool{false, false, false, false, false, true, false, true}
	ids := [...]int{1, 2, 3, 4, 1, 4, 2, 3}
	c1s := [...]string{"a", "a", "b", "a", "a", "", "b", ""}
	c2s := [...]int{10, 20, 10, 10, 30, 0, 10, 0}
	ta10 := tup.T{C1: "a", C2: 10}
	for i := 0; i < 8; i++ { // 不变量 1/2/3：逐步与推导表、逐计数比对
		var err error
		if del[i] {
			err = p.Delete(ids[i])
		} else {
			err = p.Upsert(ids[i], "k", c1s[i], c2s[i])
		}
		if err != nil || p.Distinct("k") != want[i] || p.cat.RefCount("k", ta10) != refA10[i] {
			return fmt.Errorf("dd: selfcheck step %d: err=%v", i+1, err)
		}
	}
	if p.Total() != 2 || len(p.rows) != 2 || // 不变量 1：终态只剩 (a,30)、(b,10)
		p.cat.RefCount("k", tup.T{C1: "a", C2: 30}) != 1 ||
		p.cat.RefCount("k", tup.T{C1: "b", C2: 10}) != 1 {
		return errors.New("dd: selfcheck final state mismatch")
	}
	errs := []error{p.Delete(404), p.Upsert(0, "k", "a", 1), p.Upsert(1, "", "a", 1)} // 不变量 4
	if !errors.Is(errs[0], ErrRowNotFound) || !errors.Is(errs[1], ErrInvalidRowID) ||
		!errors.Is(errs[2], ErrEmptyKey) || p.Total() != 2 || len(p.rows) != 2 {
		return errors.New("dd: selfcheck rejected-op mismatch")
	}
	if err := p.Upsert(9, "j", "z", 9); err != nil || p.Total() != 3 {
		return errors.New("dd: selfcheck engine unusable after rejection")
	}
	return tupleCheckProbe()
}

// tupleCheckProbe：m=100/1000/10000 下只触及一个元组的写操作，检查个数必须恒为
// 常数（Upsert 2、Delete 1），证明 distinct 按 0↔1 翻转增量维护而非全表重数。
func tupleCheckProbe() error {
	for _, m := range []int{100, 1000, 10000} {
		q := NewEngine()
		for i := 1; i <= m; i++ {
			if err := q.Upsert(i, "k", "c", i); err != nil {
				return err
			}
		}
		if err := q.Upsert(1, "k", "c", 2); err != nil || q.checks != 2 {
			return fmt.Errorf("dd: upsert checks=%d at m=%d", q.checks, m)
		}
		if err := q.Delete(m); err != nil || q.checks != 1 {
			return fmt.Errorf("dd: delete checks=%d at m=%d", q.checks, m)
		}
	}
	return nil
}
