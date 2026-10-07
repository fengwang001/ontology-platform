package adjudicator

// assessor 负责单类备份内部的可用范围判定：
// 输入一个类别的备份状态，输出该类别内每条记录自身是否可恢复。
// 它只看本类别，不处理任何跨类别依赖。
type assessor struct{}

// scope 为单类备份的可用范围判定结果。
type scope struct {
	// available 表示该类别整体是否可用。
	available bool
	// recoverable 为每条记录自身是否可恢复（不含依赖判定）。
	recoverable map[string]bool
}

// assess 判定单个类别备份的可用范围。
func (assessor) assess(b CategoryBackup) scope {
	s := scope{available: b.Available(), recoverable: map[string]bool{}}
	if !s.available {
		// 整体缺失或整体损坏：本类别任何记录都不可恢复，
		// 不允许凭其他类别残留的数据片段单方面猜测。
		return s
	}
	for _, r := range b.Records {
		// 部分损坏只影响损坏集合内的记录，完好部分正常推进。
		s.recoverable[r.Ref.ID] = !b.Damaged[r.Ref.ID]
	}
	return s
}
