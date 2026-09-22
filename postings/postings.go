// Package postings 维护单个 term 的「docID → 位置列表」倒排表。
package postings

import (
	"errors"
	"sort"
	"sync/atomic"
)

// ErrDuplicateDoc 表示在已含该 docID 的倒排表上重复插入。
var ErrDuplicateDoc = errors.New("postings: duplicate docID")

// ErrDocNotFound 表示倒排表中不存在该 docID。
var ErrDocNotFound = errors.New("postings: docID not found")

// lastMergeComparisons 记录最近一次归并的比较次数（非导出）。
var lastMergeComparisons atomic.Int64

// List 是一个 term 的倒排表；docID 严格升序无重复。
type List struct {
	docs []entry
}

type entry struct {
	docID     uint64
	positions []int
}

// New 创建空倒排表。
func New() *List { return &List{} }

// Add 插入一个 docID 及其位置列表。调用方保证 docID 不存在、positions 已升序。
// 位置列表会被拷贝，避免与调用方共享底层数组。
func (l *List) Add(docID uint64, positions []int) error {
	i := sort.Search(len(l.docs), func(k int) bool { return l.docs[k].docID >= docID })
	if i < len(l.docs) && l.docs[i].docID == docID {
		return ErrDuplicateDoc
	}
	ps := append([]int(nil), positions...)
	l.docs = append(l.docs, entry{})
	copy(l.docs[i+1:], l.docs[i:])
	l.docs[i] = entry{docID: docID, positions: ps}
	return nil
}

// Remove 摘除一个 docID；空表语义由调用方处理。
func (l *List) Remove(docID uint64) error {
	i := sort.Search(len(l.docs), func(k int) bool { return l.docs[k].docID >= docID })
	if i >= len(l.docs) || l.docs[i].docID != docID {
		return ErrDocNotFound
	}
	l.docs = append(l.docs[:i], l.docs[i+1:]...)
	return nil
}

// Set 替换一个 docID 的位置列表（差量更新共有 term 时使用）。
func (l *List) Set(docID uint64, positions []int) error {
	i := sort.Search(len(l.docs), func(k int) bool { return l.docs[k].docID >= docID })
	if i >= len(l.docs) || l.docs[i].docID != docID {
		return ErrDocNotFound
	}
	l.docs[i].positions = append([]int(nil), positions...)
	return nil
}

// Len 返回 docID 数量。
func (l *List) Len() int { return len(l.docs) }

// DocIDs 返回升序 docID 副本。
func (l *List) DocIDs() []uint64 {
	out := make([]uint64, len(l.docs))
	for i, e := range l.docs {
		out[i] = e.docID
	}
	return out
}

// Positions 返回某 docID 的位置列表副本。
func (l *List) Positions(docID uint64) ([]int, bool) {
	i := sort.Search(len(l.docs), func(k int) bool { return l.docs[k].docID >= docID })
	if i >= len(l.docs) || l.docs[i].docID != docID {
		return nil, false
	}
	return append([]int(nil), l.docs[i].positions...), true
}

// Intersect 返回两个倒排表 docID 的交集（短侧对长侧二分，不全量扫描长侧）。
func (l *List) Intersect(other *List) []uint64 {
	short, long := l, other
	if short.Len() > long.Len() {
		short, long = long, short
	}
	lastMergeComparisons.Store(0)
	var comparisons int64
	out := make([]uint64, 0, short.Len())
	for _, e := range short.docs {
		lo, hi := 0, long.Len()
		for lo < hi {
			mid := int(uint(lo+hi) >> 1)
			comparisons++
			if long.docs[mid].docID < e.docID {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		if lo < long.Len() && long.docs[lo].docID == e.docID {
			out = append(out, e.docID)
		}
	}
	lastMergeComparisons.Store(comparisons)
	return out
}

// Union 返回两个倒排表 docID 的并集（升序去重）。
func (l *List) Union(other *List) []uint64 {
	out := make([]uint64, 0, l.Len()+other.Len())
	i, j := 0, 0
	for i < len(l.docs) || j < len(other.docs) {
		var next uint64
		switch {
		case j >= len(other.docs):
			next = l.docs[i].docID
			i++
		case i >= len(l.docs):
			next = other.docs[j].docID
			j++
		case l.docs[i].docID < other.docs[j].docID:
			next = l.docs[i].docID
			i++
		case l.docs[i].docID > other.docs[j].docID:
			next = other.docs[j].docID
			j++
		default:
			next = l.docs[i].docID
			i++
			j++
		}
		out = append(out, next)
	}
	return out
}

// Difference 返回 l 有而 other 没有的 docID（升序）。
func (l *List) Difference(other *List) []uint64 {
	out := make([]uint64, 0, l.Len())
	for _, e := range l.docs {
		i := sort.Search(len(other.docs), func(k int) bool { return other.docs[k].docID >= e.docID })
		if i >= len(other.docs) || other.docs[i].docID != e.docID {
			out = append(out, e.docID)
		}
	}
	return out
}

// SelfCheck 校验 docID 与位置列表双重严格升序无重复。
func (l *List) SelfCheck() error {
	for i := 1; i < len(l.docs); i++ {
		if l.docs[i-1].docID >= l.docs[i].docID {
			return errors.New("postings: docID not strictly ascending")
		}
	}
	for _, e := range l.docs {
		for k := 1; k < len(e.positions); k++ {
			if e.positions[k-1] >= e.positions[k] {
				return errors.New("postings: positions not strictly ascending")
			}
		}
	}
	return nil
}
