package alarm

// Op 是一次串行重放操作：对 Key 累加 Delta。
type Op struct {
	Key   string
	Delta int64
}

// Step 是一次重放后的完整观测结果。
type Step struct {
	Op     Op
	Event  Event   // 失败时为零值
	Value  int64   // 该步之后键的累计值
	Armed  bool    // 该步之后键的开关状态
	Events []Event // 该步之后键的事件列表快照
	Err    error   // 非 nil 表示该步被整体拒绝
}

// Replay 以严格串行方式把 ops 逐步应用到一个全新的报警流上，
// 返回每一步的完整观测，作为并发执行结果的串行参照。
//
// 任一步骤失败（Err 非 nil）时停止后续步骤：失败操作已被整体拒绝，
// 返回的 Step 记录失败前的流状态（Event 为零值，Value/Armed/Events
// 为被拒绝键当时的状态）。
func Replay(threshold, hysteresis int64, ops []Op) ([]Step, error) {
	s, err := New(threshold, hysteresis)
	if err != nil {
		return nil, err
	}
	steps := make([]Step, 0, len(ops))
	for _, op := range ops {
		ev, err := s.Add(op.Key, op.Delta)
		step := Step{
			Op:    op,
			Event: ev,
			Err:   err,
		}
		if err == nil {
			step.Value = s.Value(op.Key)
			step.Armed = s.Armed(op.Key)
			step.Events = s.Events(op.Key)
			steps = append(steps, step)
			continue
		}
		if op.Key != "" {
			step.Value = s.Value(op.Key)
			step.Armed = s.Armed(op.Key)
			step.Events = s.Events(op.Key)
		}
		steps = append(steps, step)
		return steps, err
	}
	return steps, nil
}
