package lifecycle

// planStep 是一次迁移（根触发或级联随迁）在处理单元中的规划结果。
type planStep struct {
	inst      *Instance
	trans     *Transition
	cascadeOf string
	basis     []string
	fired     FiredStep
}
