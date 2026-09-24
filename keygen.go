package ontology

import "strconv"

// GenerateKeys 以固定方式生成 n 个 key，供测试与演示程序使用，
// 保证每次运行（包括跨进程）得到的 key 集合完全相同。
func GenerateKeys(n int) []string {
	keys := make([]string, n)
	for i := range keys {
		keys[i] = "key-" + strconv.Itoa(i)
	}
	return keys
}
