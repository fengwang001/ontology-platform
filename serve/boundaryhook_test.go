package serve_test

import (
	"ontology/multipart"
	"ontology/serve"
)

// deterministicBoundary 让所有测试构建共享同一边界串，便于逐字节对照。
// 若内容冲突（极少），依次尝试后缀编号，仍然保证不冲突。
func deterministicBoundary(content []byte, maxAttempts int) (string, error) {
	for i := 0; i < maxAttempts; i++ {
		candidate := "TESTBND" + string(rune('a'+i)) + "000000000000000000000000"
		if !multipart.ContainsBoundary(content, candidate) {
			return candidate, nil
		}
	}
	return "", serve.ErrBoundaryAttempts
}

func init() {
	// serve 通过包级变量暴露选择器，仅测试替换为确定性实现。
	serve.SetBoundaryChooser(deterministicBoundary)
}
