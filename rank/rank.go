// Package rank 定义 Top-N 的排序规则，并按该顺序有序维护全部存活行。
// 不依赖其他包。
package rank

import (
	"fmt"
	"math/bits"
	"sort"
)

// Row 是一个存活行：Key 唯一，Score 决定名次。
type Row struct {
	Key   string
	Score int64
}

// Change 是输出给下游的一条变更日志：Add=true 表示 +(进入前 N)，
// Add=false 表示 −(离开前 N)。Op 复用同一结构描述输入。
type Change struct {
	Row Row
	Add bool
}

// Ordered 是按 Less 严格有序维护的存活行集合。
type Ordered struct {
	rows []Row
	idx  map[string]int

	// cmpCount 记录最近一次操作中比较过的存活行个数（非导出）：
	// 定位插入/删除位置、定位补位行或被挤出行的全部比较都计入。
	cmpCount int
}

// New 创建空的有序集合。
func New() *Ordered {
	return &Ordered{idx: map[string]int{}}
}

// Less 定义排序：Score 降序；Score 相同时 Key 按字节序升序在前。
func Less(a, b Row) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	return a.Key < b.Key
}

// Len 返回存活行数。
func (o *Ordered) Len() int { return len(o.rows) }

// Has 报告 key 是否有存活行。
func (o *Ordered) Has(key string) bool {
	_, ok := o.idx[key]
	return ok
}

// Lookup 返回 key 对应存活行。
func (o *Ordered) Lookup(key string) (Row, bool) {
	p, ok := o.idx[key]
	if !ok {
		return Row{}, false
	}
	return o.rows[p], true
}

// At 返回排序后第 i 个位置的行。
func (o *Ordered) At(i int) Row { return o.rows[i] }

// Insert 加入一行（调用方须保证 Key 不存在），返回其排序位置。
func (o *Ordered) Insert(r Row) int {
	o.cmpCount = 0
	p := sort.Search(len(o.rows), func(i int) bool {
		o.cmpCount++ // 二分中的每次比较都计数
		// 第一个「不排在 r 之前」的位置，即 r 的插入点。
		return !Less(o.rows[i], r)
	})
	o.rows = append(o.rows, Row{})
	copy(o.rows[p+1:], o.rows[p:])
	o.rows[p] = r
	for i := p; i < len(o.rows); i++ { // 位置平移是数据搬运，不是比较
		o.idx[o.rows[i].Key] = i
	}
	return p
}

// Remove 按 key 移除存活行，返回被移除的行。
func (o *Ordered) Remove(key string) (Row, bool) {
	o.cmpCount = 0
	p, ok := o.idx[key] // 经 Key 索引直接定位，不做行比较
	if !ok {
		return Row{}, false
	}
	r := o.rows[p]
	o.rows = append(o.rows[:p], o.rows[p+1:]...)
	delete(o.idx, key)
	for i := p; i < len(o.rows); i++ {
		o.idx[o.rows[i].Key] = i
	}
	return r, true
}

// Snapshot 返回按排序序排列的全部存活行副本。
func (o *Ordered) Snapshot() []Row {
	out := make([]Row, len(o.rows))
	copy(out, o.rows)
	return out
}

// bound 是题目给定的单次操作比较个数上界：4·⌈log₂(m+1)⌉ + 4。
// ⌈log₂(m+1)⌉ 恰等于 bits.Len(m)。
func bound(m int) int { return 4*bits.Len(uint(m)) + 4 }

// SelfTest 在包内按多档规模构造存活行，执行一次进榜新增与一次榜内撤回，
// 核验比较个数不随规模线性增长。只返回通过与否，计数器数值不跨包边界。
func SelfTest() error {
	for _, m := range []int{100, 500, 2000, 10000} {
		o := New()
		for i := 0; i < m; i++ { // Key 各不相同；部分分数并列（对 50 取模）
			o.Insert(Row{Key: fmt.Sprintf("k%05d", i), Score: int64(i % 50)})
		}
		o.Insert(Row{Key: "zzz-new-high", Score: 1 << 40}) // 必然进榜的新增
		if o.cmpCount > bound(m) {
			return fmt.Errorf("rank: insert comparisons %d exceed bound %d at m=%d", o.cmpCount, bound(m), m)
		}
		top := o.At(0)
		o.Remove(top.Key) // 撤回榜内行（经 Key 索引 O(1) 定位，无行比较）
		if o.cmpCount > bound(m) {
			return fmt.Errorf("rank: remove comparisons %d exceed bound %d at m=%d", o.cmpCount, bound(m), m)
		}
	}
	return nil
}

// Clone 返回深拷贝。
func (o *Ordered) Clone() *Ordered {
	c := &Ordered{rows: make([]Row, len(o.rows)), idx: make(map[string]int, len(o.idx))}
	copy(c.rows, o.rows)
	for k, v := range o.idx {
		c.idx[k] = v
	}
	return c
}
