package serve

// SetBoundaryChooser 仅供测试替换边界选择策略。
func SetBoundaryChooser(f func([]byte, int) (string, error)) {
	boundaryChooser = f
}
