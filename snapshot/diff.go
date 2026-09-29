package snapshot

// validateSorted 校验快照按键严格升序；从前往后扫描，
// 第一处违规决定错误类别：重复键返回 ErrDuplicateKey，否则返回 ErrUnsorted。
func validateSorted(entries []Entry) error { return nil }

// Diff 对旧、新两份有序快照做双指针归并差分。
// 任一输入校验失败或变更条数超过 MaxChanges 时整体拒绝。
func Diff(oldSnap, newSnap []Entry, cfg Config, log Logger) ([]Change, error) {
	return nil, nil
}

// Apply 将变更日志按序应用到有序快照，返回新快照；
// 日志与快照均被校验，非法输入返回错误。
func Apply(base []Entry, changes []Change) ([]Entry, error) { return nil, nil }
