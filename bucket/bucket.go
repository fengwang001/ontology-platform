// Package bucket 提供签名到向量 ID 列表的桶表，以及多表并存的不可变集合。
package bucket

// Table 把一张 LSH 表的签名映射到落在该桶中的向量 ID 列表。
// 构建完成后只读，可安全地被多协程并发查询。
type Table struct {
	m map[uint64][]int
}

// NewTable 返回空桶表。
func NewTable() *Table { return &Table{m: make(map[uint64][]int)} }

// Add 把向量 id 加入签名 sig 对应的桶（仅构建期调用）。
func (t *Table) Add(sig uint64, id int) { t.m[sig] = append(t.m[sig], id) }

// Get 返回签名 sig 桶中的全部向量 ID；桶不存在时返回 nil。
func (t *Table) Get(sig uint64) []int { return t.m[sig] }

// NumBuckets 返回非空桶数。
func (t *Table) NumBuckets() int { return len(t.m) }

// Buckets 返回底层映射（只读使用，供持久化遍历）。
func (t *Table) Buckets() map[uint64][]int { return t.m }

// Set 是多表并存的不可变集合：查询时并集各表候选，构建方整体替换发布。
type Set struct {
	Tables []*Table
}

// Candidates 返回各表中与 sigs 对应桶的向量 ID 并集（保持首次出现顺序）。
func (s *Set) Candidates(sigs []uint64) []int {
	seen := make(map[int]struct{})
	var out []int
	for i, t := range s.Tables {
		for _, id := range t.Get(sigs[i]) {
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				out = append(out, id)
			}
		}
	}
	return out
}
