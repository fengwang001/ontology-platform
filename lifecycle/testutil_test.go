package lifecycle

// setFailAfterSteps 仅供测试：在提交到第 n 个生效环节后人为失败，
// 以验证反向整体回滚。
func (e *Engine) setFailAfterSteps(n int) {
	e.fp.failAfterSteps = n
}
