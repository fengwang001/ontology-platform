// Package api 是对外门面：New、Insert/Delete/Locate/Ranges/Compact
// 的包装与 SelfCheck。依赖 store。
package api

import (
	"errors"
	"fmt"

	"ontology/store"
)

// Range 是分区区间与负载的只读快照。
type Range = store.Range

// 三类可判定的哨兵错误（与 store 层同一组，互不相同）。
var (
	ErrInvalidParams = store.ErrInvalidParams
	ErrOutOfRange    = store.ErrOutOfRange
	ErrNotFound      = store.ErrNotFound
)

// Table 是一个 range 分区表：键空间 [low,high) 上的自动分裂/合并分区集合。
type Table struct {
	s *store.Store
}

// New 构造分区表；参数非法返回 ErrInvalidParams。
func New(low, high int64, splitThreshold, mergeThreshold int) (*Table, error) {
	s, err := store.New(low, high, splitThreshold, mergeThreshold)
	if err != nil {
		return nil, err
	}
	return &Table{s: s}, nil
}

// Insert 插入键；键越界返回 ErrOutOfRange。热点分区自动分裂。
func (t *Table) Insert(key int64) error { return t.s.Insert(key) }

// Delete 删除键；键不存在返回 ErrNotFound。
func (t *Table) Delete(key int64) error { return t.s.Delete(key) }

// Locate 定位键所在分区；键越界返回 ErrOutOfRange。
func (t *Table) Locate(key int64) (Range, error) { return t.s.Locate(key) }

// Ranges 返回当前分区列表（按 lo 升序）。
func (t *Table) Ranges() []Range { return t.s.Ranges() }

// Compact 贪心单遍合并相邻冷区。
func (t *Table) Compact() { t.s.Compact() }

// SelfCheck 对一组内置操作序列（第三节十一步场景）核验四条不变量：
// 键不丢不重、范围不变量、load 与朴素重算一致、失败不留痕。
// 在内部全新实例上执行，不影响接收者状态。
func (t *Table) SelfCheck() error {
	s, err := store.New(0, 16, 3, 2)
	if err != nil {
		return err
	}
	live := map[int64]bool{}
	ops := []struct{ ins, del int64 }{{5, -1}, {11, -1}, {8, -1}, {14, -1}, {2, -1}, {6, -1}, {12, -1}, {10, -1}, {-1, 5}, {-1, 2}, {-1, -1}}
	for _, o := range ops {
		switch {
		case o.ins >= 0:
			if err := s.Insert(o.ins); err != nil {
				return fmt.Errorf("api: 自检 Insert: %w", err)
			}
			live[o.ins] = true
		case o.del >= 0:
			if err := s.Delete(o.del); err != nil {
				return fmt.Errorf("api: 自检 Delete: %w", err)
			}
			delete(live, o.del)
		default:
			s.Compact()
		}
		if !invariants(s, live) {
			return errors.New("api: 自检失败: 键不丢不重/范围不变量/朴素重算 被破坏")
		}
	}
	before := fmt.Sprint(s.Ranges())
	if err := s.Insert(-1); !errors.Is(err, ErrOutOfRange) {
		return errors.New("api: 自检失败: Insert 越界未报键越界")
	}
	if _, err := s.Locate(16); !errors.Is(err, ErrOutOfRange) {
		return errors.New("api: 自检失败: Locate 越界未报键越界")
	}
	if err := s.Delete(3); !errors.Is(err, ErrNotFound) {
		return errors.New("api: 自检失败: Delete 未报键不存在")
	}
	if _, err := New(0, 0, 3, 2); !errors.Is(err, ErrInvalidParams) {
		return errors.New("api: 自检失败: New 未报参数非法")
	}
	if fmt.Sprint(s.Ranges()) != before {
		return errors.New("api: 自检失败: 被拒操作改变了状态")
	}
	return nil
}

// invariants 用公开接口核验：范围不变量、键不丢不重、load 与朴素重算一致。
func invariants(s *store.Store, live map[int64]bool) bool {
	rs := s.Ranges()
	if len(rs) == 0 || rs[0].Lo != 0 || rs[len(rs)-1].Hi != 16 {
		return false
	}
	total := 0
	for i, r := range rs {
		if r.Lo >= r.Hi || (i > 0 && rs[i-1].Hi != r.Lo) {
			return false
		}
		naive := 0
		for k := range live {
			if r.Lo <= k && k < r.Hi {
				naive++
			}
		}
		if naive != r.Load {
			return false
		}
		total += r.Load
	}
	if total != len(live) {
		return false
	}
	for k := range live {
		rg, err := s.Locate(k)
		if err != nil || rg.Lo > k || k >= rg.Hi {
			return false
		}
	}
	return true
}
