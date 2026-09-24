// Package bucket 提供“签名 -> 向量 ID 列表”的桶表及多表并存。
package bucket

import "sort"

// Table 是一张表的桶映射；签名以定长 []byte 的字符串形式做键。
type Table struct {
	m map[string][]int
}

// NewTable 创建空桶表。
func NewTable() *Table {
	return &Table{m: make(map[string][]int)}
}

// Add 将向量 id 放入签名 sig 对应的桶。
func (t *Table) Add(sig []byte, id int) {
	k := string(sig)
	t.m[k] = append(t.m[k], id)
}

// Lookup 返回与 sig 同桶的全部向量 ID（顺序为插入顺序）。
func (t *Table) Lookup(sig []byte) []int {
	return t.m[string(sig)]
}

// Len 返回非空桶数。
func (t *Table) Len() int { return len(t.m) }

// Signatures 返回全部桶签名（排序后，便于落盘与核对）。
func (t *Table) Signatures() [][]byte {
	keys := make([]string, 0, len(t.m))
	for k := range t.m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([][]byte, len(keys))
	for i, k := range keys {
		out[i] = []byte(k)
	}
	return out
}

// IDs 返回某桶的 ID 列表。
func (t *Table) IDs(sig []byte) []int { return t.m[string(sig)] }

// RemoveMissing 删除桶表中不在 valid 集合内的引用，返回删除条数。
// 变空的桶一并移除；恢复后保证无悬挂 ID。
func (t *Table) RemoveMissing(valid map[int]bool) int {
	dropped := 0
	for k, ids := range t.m {
		keep := ids[:0]
		for _, id := range ids {
			if valid[id] {
				keep = append(keep, id)
			} else {
				dropped++
			}
		}
		if len(keep) == 0 {
			delete(t.m, k)
		} else {
			t.m[k] = keep
		}
	}
	return dropped
}

// MultiTable 是 L 张桶表的集合。
type MultiTable struct {
	tables []*Table
}

// NewMultiTable 创建 n 张空桶表。
func NewMultiTable(n int) *MultiTable {
	ts := make([]*Table, n)
	for i := range ts {
		ts[i] = NewTable()
	}
	return &MultiTable{tables: ts}
}

func (m *MultiTable) Len() int           { return len(m.tables) }
func (m *MultiTable) Table(i int) *Table { return m.tables[i] }

// Add 在第 l 张表中放入 (sig, id)。
func (m *MultiTable) Add(l int, sig []byte, id int) {
	m.tables[l].Add(sig, id)
}

// Candidates 汇总各表同桶候选的并集；use 指定哪些表参与。
func (m *MultiTable) Candidates(sigs [][]byte, use []bool) []int {
	seen := make(map[int]struct{})
	var out []int
	for l, sig := range sigs {
		if l < len(use) && !use[l] {
			continue
		}
		for _, id := range m.tables[l].Lookup(sig) {
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				out = append(out, id)
			}
		}
	}
	return out
}

// RemoveMissing 对所有表剔除无效 ID，返回总删除条数。
func (m *MultiTable) RemoveMissing(valid map[int]bool) int {
	n := 0
	for _, t := range m.tables {
		n += t.RemoveMissing(valid)
	}
	return n
}
