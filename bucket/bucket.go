// Package bucket 维护多张「签名 -> 向量 ID 列表」的 LSH 桶表。
package bucket

import "ontology/hyper"

// ID 是被索引向量的外部标识。
type ID = int32

// Table 是一张桶表：签名到该桶内向量 ID 列表。
type Table map[hyper.Signature][]ID

// Tables 是多张独立桶表的集合。
type Tables []Table

// New 创建 tables 张空桶表。
func New(tables int) Tables {
	ts := make(Tables, tables)
	for i := range ts {
		ts[i] = make(Table)
	}
	return ts
}

// Add 把 id 放入第 table 张表签名 sig 对应的桶。
func (ts Tables) Add(table int, sig hyper.Signature, id ID) {
	ts[table][sig] = append(ts[table][sig], id)
}

// Get 返回第 table 张表中签名 sig 桶内的 ID（只读）。
func (ts Tables) Get(table int, sig hyper.Signature) []ID {
	return ts[table][sig]
}

// BucketCount 返回某张表的桶数。
func (ts Tables) BucketCount(table int) int { return len(ts[table]) }

// Entries 遍历某张表的全部桶，用于落盘。
func (ts Tables) Entries(table int) []Entry {
	out := make([]Entry, 0, len(ts[table]))
	for sig, ids := range ts[table] {
		out = append(out, Entry{Sig: sig, IDs: ids})
	}
	return out
}

// Entry 是一个桶的签名与其成员。
type Entry struct {
	Sig hyper.Signature
	IDs []ID
}

// Snapshot 返回深拷贝，供建索引完成后原子发布。
func (ts Tables) Snapshot() Tables {
	cp := make(Tables, len(ts))
	for t := range ts {
		cp[t] = make(Table, len(ts[t]))
		for sig, ids := range ts[t] {
			dst := make([]ID, len(ids))
			copy(dst, ids)
			cp[t][sig] = dst
		}
	}
	return cp
}
