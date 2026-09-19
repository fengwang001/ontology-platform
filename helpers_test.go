package ontology

import "fmt"

// keyOf 生成可排序、长度一致的测试主键（k000、k001、...）。
func keyOf(i int) string {
	return fmt.Sprintf("k%03d", i)
}
