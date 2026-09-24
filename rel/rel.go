// Package rel 是单张多重集表：按 K 建索引的 (K,V)→多重性。
// 不依赖其他任何包。
package rel

// Row 是一条带符号的变更行：Sign=+1 插入一次，Sign=-1 删除一次。
type Row struct {
	K    int64
	V    string
	Sign int
}

// Table 是多重集表，零值不可用，用 New 构造。
// 内部按 K 分桶，桶内 V→多重性；多重性降到 0 的行立即删除。
type Table struct {
	byK map[int64]map[string]int64
}

// New 创建一张空表。
func New() *Table {
	return &Table{byK: make(map[int64]map[string]int64)}
}

// Mult 返回 (k,v) 当前的多重性，不存在返回 0。
func (t *Table) Mult(k int64, v string) int64 {
	return t.byK[k][v]
}

// Add 在 (k,v) 上叠加带符号增量 d（调用方须保证结果非负）。
// 叠加后多重性为 0 时删除该行；桶空时删除整个 K 桶，不占内存。
func (t *Table) Add(k int64, v string, d int64) {
	bucket := t.byK[k]
	if bucket == nil {
		bucket = make(map[string]int64)
		t.byK[k] = bucket
	}
	m := bucket[v] + d
	if m == 0 {
		delete(bucket, v)
		if len(bucket) == 0 {
			delete(t.byK, k)
		}
		return
	}
	bucket[v] = m
}

// Match 遍历 K=k 的全部 (V, 多重性) 行；桶不存在时什么都不做。
// 回调内不得修改本表。这是按 K 索引取匹配行的唯一入口，
// djoin 借此保证差分计算只碰相关 K 桶，不扫描整张对侧表。
func (t *Table) Match(k int64, fn func(v string, mult int64)) {
	for v, m := range t.byK[k] {
		fn(v, m)
	}
}

// Snapshot 返回 (k,v,mult) 的完整快照副本，多重性均为非零正数。
// 供全量重算对照使用；差分计算路径不使用它。
func (t *Table) Snapshot() []Triple {
	out := make([]Triple, 0, len(t.byK))
	for k, bucket := range t.byK {
		for v, m := range bucket {
			out = append(out, Triple{K: k, V: v, Mult: m})
		}
	}
	return out
}

// Triple 是快照中的一行。
type Triple struct {
	K    int64
	V    string
	Mult int64
}
