// Package index 提供按类别分桶、按 (到期日, 编号) 排序的到期索引。
//
// 设计取舍：每个类别一个有序切片，插入/删除用二分查找定位（O(log n) 次比较，
// 因切片搬移整体 O(n)），范围查询仅做 O(log n) 次比较定位上界后顺序取出
// 命中前缀，比较与访问次数为 O(log n + k)，k 为命中条数，与对象总数无关。
// 由于类别集合固定且很小（锅炉/压力容器/安全阀/压力表），按类别分桶使
// 预警查询可以只对每个桶用各自的预警窗口定位，避免扫描全表。
package index

import (
	"sort"

	"ontology/domain"
)

// Entry 是索引中的一条记录。
type Entry struct {
	Expiry int
	ID     string
}

// Bucket 是单个类别的有序索引。
type Bucket struct {
	entries []Entry // 按 (Expiry, ID) 升序
}

// locate 返回 key 应处的位置（第一个不小于 key 的下标），并累计比较次数。
func (b *Bucket) locate(key Entry) (pos int, cmps int) {
	pos = sort.Search(len(b.entries), func(i int) bool {
		cmps++
		e := b.entries[i]
		if e.Expiry != key.Expiry {
			return e.Expiry >= key.Expiry
		}
		return e.ID >= key.ID
	})
	return pos, cmps
}

// Upsert 插入或更新一条记录。若同 ID 记录已存在，须先 Remove。
func (b *Bucket) Upsert(e Entry) (cmps int) {
	pos, cmps := b.locate(e)
	b.entries = append(b.entries, Entry{})
	copy(b.entries[pos+1:], b.entries[pos:])
	b.entries[pos] = e
	return cmps
}

// Remove 删除一条精确匹配的记录；不存在时不做任何事。
func (b *Bucket) Remove(e Entry) (cmps int) {
	pos, cmps := b.locate(e)
	if pos < len(b.entries) && b.entries[pos] == e {
		b.entries = append(b.entries[:pos], b.entries[pos+1:]...)
	}
	return cmps
}

// Range 返回 lo <= Expiry <= hi 的全部记录（按 (Expiry, ID) 升序），
// 以及本次查询的比较次数与访问元素数，用于验证复杂度上界。
// 两端都用二分定位，开销 O(log n + k)，k 为命中条数；
// 区间外的记录（如已超期对象）不会被访问。
func (b *Bucket) Range(lo, hi int) (out []Entry, cmps, visited int) {
	start, c1 := b.locate(Entry{Expiry: lo, ID: ""})
	end, c2 := b.locate(Entry{Expiry: hi + 1, ID: ""})
	cmps = c1 + c2
	out = make([]Entry, end-start)
	copy(out, b.entries[start:end])
	return out, cmps, len(out)
}

// Len 返回桶内记录数。
func (b *Bucket) Len() int { return len(b.entries) }

// Index 按类别分桶的到期索引。
type Index struct {
	buckets map[domain.Category]*Bucket
}

// New 创建空索引。
func New() *Index { return &Index{buckets: make(map[domain.Category]*Bucket)} }

func (ix *Index) bucket(cat domain.Category) *Bucket {
	b, ok := ix.buckets[cat]
	if !ok {
		b = &Bucket{}
		ix.buckets[cat] = b
	}
	return b
}

// Upsert 在指定类别桶中插入/更新记录。
func (ix *Index) Upsert(cat domain.Category, e Entry) { ix.bucket(cat).Upsert(e) }

// Remove 从指定类别桶删除记录。
func (ix *Index) Remove(cat domain.Category, e Entry) { ix.bucket(cat).Remove(e) }

// Range 查询指定类别桶中 lo <= Expiry <= hi 的记录。
func (ix *Index) Range(cat domain.Category, lo, hi int) (out []Entry, cmps, visited int) {
	b, ok := ix.buckets[cat]
	if !ok {
		return nil, 0, 0
	}
	return b.Range(lo, hi)
}

// Len 返回指定类别桶的记录数。
func (ix *Index) Len(cat domain.Category) int {
	if b, ok := ix.buckets[cat]; ok {
		return b.Len()
	}
	return 0
}
