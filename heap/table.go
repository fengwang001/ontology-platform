// Package heap 实现带可见性映射（visibility map）的堆表与仅索引扫描器。
//
// 堆表按页组织，每页固定数量的行槽。每页维护一个全可见位：
// 置位（仅由 Vacuum 完成）：页内剩余每一行都满足 xmin < h 且 xmax = 0，
// 空页视为真；清除：Insert 或 Delete 触及该页时立即清除。
// 扫描时全可见位为真的页上的条目直接产出、免回表。
package heap

import (
	"sort"
	"sync"
)

// Row 是堆表中的一行。Xmax 为 0 表示未删除。
type Row struct {
	Key  string
	Xmin int64
	Xmax int64
}

// Entry 是索引条目，也是 Scan 产出的结果项，按 (Key, Page, Slot) 升序排列。
type Entry struct {
	Key  string
	Page int
	Slot int
}

// ScanResult 是 Scan 的返回结果。
type ScanResult struct {
	// Rows 为产出的 (key, 页, 槽) 列表，按 (key, 页, 槽) 升序。
	Rows []Entry
	// Fetches 为回表次数（条目所在页全可见位为假的次数）。
	Fetches int
	// Skips 为免回表次数（条目所在页全可见位为真的次数）。
	Skips int
}

// Table 是带可见性映射的堆表。所有方法可并发调用，
// 结果等价于某个串行顺序（内部以互斥锁串行化）。
type Table struct {
	mu sync.Mutex

	pages  [][]*Row // pages[p][s] 为 nil 表示空槽
	allVis []bool   // 每页全可见位
	index  []Entry  // 按 (Key, Page, Slot) 升序

	snapshots  map[int]int64 // 快照编号 -> 快照值
	nextSnapID int           // 下一个快照编号（从 1 起）
	maxH       int64         // 全局最大已用 h
}

// New 构造 P 页、每页 C 个行槽的堆表。P、C 均不小于 1。
func New(p, c int) (*Table, error) {
	if p < 1 {
		return nil, ErrInvalidPageCount
	}
	if c < 1 {
		return nil, ErrInvalidSlotCount
	}
	pages := make([][]*Row, p)
	for i := range pages {
		pages[i] = make([]*Row, c)
	}
	return &Table{
		pages:      pages,
		allVis:     make([]bool, p),
		snapshots:  make(map[int]int64),
		nextSnapID: 1,
	}, nil
}

// AllVisible 返回某页的全可见位（测试与调试用）。
func (t *Table) AllVisible(page int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.allVis[page]
}

// MaxH 返回全局最大已用 h（测试与调试用）。
func (t *Table) MaxH() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.maxH
}

// indexSearch 返回 key 在索引中的插入位置（按 (Key, Page, Slot) 升序）。
func indexLess(a, b Entry) bool {
	if a.Key != b.Key {
		return a.Key < b.Key
	}
	if a.Page != b.Page {
		return a.Page < b.Page
	}
	return a.Slot < b.Slot
}

func (t *Table) indexInsert(e Entry) {
	i := sort.Search(len(t.index), func(i int) bool { return !indexLess(t.index[i], e) })
	t.index = append(t.index, Entry{})
	copy(t.index[i+1:], t.index[i:])
	t.index[i] = e
}

func (t *Table) indexRemove(e Entry) {
	i := sort.Search(len(t.index), func(i int) bool { return !indexLess(t.index[i], e) })
	if i < len(t.index) && t.index[i] == e {
		t.index = append(t.index[:i], t.index[i+1:]...)
	}
}
