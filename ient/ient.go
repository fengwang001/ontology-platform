// Package ient 维护按 (F, PK) 全序有序的二级索引项集合。
// 不依赖其他包。
package ient

import (
	"fmt"
	"math/bits"
	"sort"
)

// item 是一条索引项：(F, PK) 二元组。
type item struct {
	f  int64
	pk string
}

func less(a, b item) bool {
	if a.f != b.f {
		return a.f < b.f
	}
	return a.pk < b.pk
}

// Set 是有序索引项集合。同一 (F, PK) 至多出现一次。
// checked 记录最近一次 Range/Eq 检查过的索引项个数，非导出，不外泄数值。
type Set struct {
	items   []item
	checked int
}

// New 返回空集合。
func New() *Set { return &Set{} }

// lower 返回首个不小于 (f, pk) 的下标，比较次数计入 checked。
func (s *Set) lower(f int64, pk string) int {
	lo, hi := 0, len(s.items)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		s.checked++
		if less(s.items[mid], item{f, pk}) {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// Add 插入 (f, pk)；已存在则无操作，不产生重复项。
func (s *Set) Add(f int64, pk string) {
	i := s.lower(f, pk)
	if i < len(s.items) && s.items[i].f == f && s.items[i].pk == pk {
		return
	}
	s.items = append(s.items, item{})
	copy(s.items[i+1:], s.items[i:])
	s.items[i] = item{f, pk}
}

// Del 删除 (f, pk)；不存在则无操作。
func (s *Set) Del(f int64, pk string) {
	i := s.lower(f, pk)
	if i < len(s.items) && s.items[i].f == f && s.items[i].pk == pk {
		s.items = append(s.items[:i], s.items[i+1:]...)
	}
}

// Range 返回 F 落在 [lo, hi) 内的主键列表，按主键字典序升序。
// lo >= hi 不是错误，返回空列表。
func (s *Set) Range(lo, hi int64) []string {
	s.checked = 0
	out := []string{}
	if lo >= hi {
		return out
	}
	for i := s.lower(lo, ""); i < len(s.items); i++ {
		s.checked++
		if s.items[i].f >= hi {
			break
		}
		out = append(out, s.items[i].pk)
	}
	sort.Strings(out)
	return out
}

// Eq 返回 F == f 的主键列表，按主键升序；语义等价于 Range(f, f+1)。
func (s *Set) Eq(f int64) []string {
	s.checked = 0
	out := []string{}
	for i := s.lower(f, ""); i < len(s.items); i++ {
		s.checked++
		if s.items[i].f != f {
			break
		}
		out = append(out, s.items[i].pk)
	}
	return out
}

// SelfCheck 在内部构造 m 个索引项并执行只命中 1 个结果的 Range，
// 核验检查个数不超过 2*log2(m)+3 的对数量级上界。
// 只返回成败，计数器数值不经由任何导出接口外泄。
func (s *Set) SelfCheck() error {
	const m = 10000
	t := New()
	for i := 0; i < m; i++ {
		t.Add(int64(i*3), fmt.Sprintf("pk%05d", i)) // F 互异，保证命中唯一
	}
	got := t.Range(29997, 29998) // 只命中 F=29997 一项
	if len(got) != 1 || got[0] != "pk09999" {
		return fmt.Errorf("ient: selfcheck range got %v", got)
	}
	if bound := 2*(bits.Len(uint(m))-1) + 3; t.checked > bound {
		return fmt.Errorf("ient: checked items exceed log bound %d", bound)
	}
	return nil
}
