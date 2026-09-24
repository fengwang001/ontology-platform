// Package ient 维护按 (F, PK) 全序排列的有序索引项集合，不依赖其他包。
package ient

import (
	"sort"
	"sync/atomic"
)

// Item 是一条索引项：字段值 F 与主键 PK 的二元组。
type Item struct {
	F  int64
	PK string
}

// Set 是有序索引项集合，items 始终按 (F 升序, PK 升序) 全序存放。
type Set struct {
	items []Item
	// lastChecked 记录最近一次 Range/Eq 扫描检查过的索引项个数。
	// 非导出字段，不出现在任何公开接口中，仅供包内测试读取。
	lastChecked atomic.Int64
}

// locate 二分定位 (f, pk)：返回插入位置与是否已存在。
func (s *Set) locate(f int64, pk string) (int, bool) {
	i := sort.Search(len(s.items), func(i int) bool {
		it := s.items[i]
		return it.F > f || (it.F == f && it.PK >= pk)
	})
	return i, i < len(s.items) && s.items[i].F == f && s.items[i].PK == pk
}

// Insert 插入 (f, pk)，调用方保证同一 (F, PK) 尚未存在。
func (s *Set) Insert(f int64, pk string) {
	i, _ := s.locate(f, pk)
	s.items = append(s.items, Item{})
	copy(s.items[i+1:], s.items[i:])
	s.items[i] = Item{F: f, PK: pk}
}

// Delete 删除 (f, pk)，调用方保证其存在。
func (s *Set) Delete(f int64, pk string) {
	i, ok := s.locate(f, pk)
	if !ok {
		return
	}
	s.items = append(s.items[:i], s.items[i+1:]...)
}

// Range 返回 F 落在左闭右开区间 [lo, hi) 内的主键列表，按主键字典序升序。
// lo >= hi 不是错误，返回空列表。
func (s *Set) Range(lo, hi int64) []string {
	if lo >= hi {
		s.lastChecked.Store(0)
		return nil
	}
	i, _ := s.locate(lo, "")
	var out []string
	n := 0
	for ; i < len(s.items) && s.items[i].F < hi; i++ {
		n++
		out = append(out, s.items[i].PK)
	}
	if i < len(s.items) {
		n++ // 终止循环时越界检查的那一项
	}
	s.lastChecked.Store(int64(n))
	sort.Strings(out)
	return out
}

// Eq 返回 F==f 的主键列表，等价于 Range(f, f+1)。
func (s *Set) Eq(f int64) []string { return s.Range(f, f+1) }
