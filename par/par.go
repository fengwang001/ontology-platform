// Package par 把缓冲区按任意字节偏移切 K 段并行解析。
package par

import "ontology/table"

// Parse 把 buf 切成 K 段并行解析，结果与 table.Parse 完全相同。
func Parse(buf []byte, k int, lim table.Limits) (table.Table, error) {
	return table.Table{}, nil
}

// ParseWithCount 同 Parse，额外返回状态机处理字节总数。
func ParseWithCount(buf []byte, k int, lim table.Limits) (table.Table, int, error) {
	return table.Table{}, 0, nil
}
