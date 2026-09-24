// Package writer 最小引号回写：只在必要时加引号。
package writer

import "ontology/cell"

// Write 把记录写成 CSV 字节流，行分隔为 \n，末尾恰好一个 \n。
func Write(records [][]cell.Cell) []byte { return nil }
