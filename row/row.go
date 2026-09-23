// Package row 定义分页数据集中的一行：一个 float64 排序值加一个唯一字符串 ID，
// 以及在二者上构成的复合键全序。
package row

import "sort"

// Row 是数据集中的一行。V 为排序值，ID 在同一数据集内唯一。
type Row struct {
	V  float64
	ID string
}

// Key 是行的复合排序键 (V, ID)。
type Key struct {
	V  float64
	ID string
}

// KeyOf 返回某行的复合键。
func KeyOf(r Row) Key { return Key{V: r.V, ID: r.ID} }

// Less 报告复合键 a 是否严格小于 b：先比排序值，相等再比 ID 字典序。
// ID 唯一，因此 (V, ID) 在数据集上构成全序。
func Less(a, b Key) bool {
	if a.V != b.V {
		return a.V < b.V
	}
	return a.ID < b.ID
}

// Equal 报告两个复合键是否相同。
func Equal(a, b Key) bool { return a.V == b.V && a.ID == b.ID }

// Sort 将行切片按复合键升序原地排序。
func Sort(rows []Row) {
	sort.Slice(rows, func(i, j int) bool {
		return Less(KeyOf(rows[i]), KeyOf(rows[j]))
	})
}

// Keys 返回行切片对应的复合键切片，顺序不变。
func Keys(rows []Row) []Key {
	keys := make([]Key, len(rows))
	for i := range rows {
		keys[i] = KeyOf(rows[i])
	}
	return keys
}
