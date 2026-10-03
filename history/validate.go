package history

// validateBatch 校验 Append 的参数：工作流名非空，每条事件种类合法且
// 对应的 name/pid 非空。expect 与容量在持锁后另行检查。
func validateBatch(wf []byte, events []Event) error {
	if len(wf) == 0 {
		return ErrArgument
	}
	for _, e := range events {
		switch e.Kind {
		case KindStep:
			if len(e.Name) == 0 {
				return ErrArgument
			}
		case KindMarker:
			if len(e.Pid) == 0 {
				return ErrArgument
			}
		default:
			return ErrArgument
		}
	}
	return nil
}
