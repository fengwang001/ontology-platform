// Package writer 把表按最小引号规则回写为 CSV 字节。
package writer

import (
	"strings"

	"ontology/cell"
)

// Write 回写整张表；记录间以 \n 连接，无尾换行。
// 仅在必要时加引号：值含逗号、引号、\r、\n，或单列记录唯一字段为空。
func Write(records [][]cell.Cell) []byte {
	var b strings.Builder
	_ = b
	return nil
}
