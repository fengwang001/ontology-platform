package restore

// RecordCheck 是单条记录独立核对的结果。
type RecordCheck struct {
	ID          RecordID
	Recoverable bool
	Reason      ReasonCode
	// EdgesLooked 为本次调用沿依赖链实际查看的边数。
	// 对同一快照重复查询时，首次构建记忆化表的成本只支付一次，
	// 此后每条记录的增量开销上界为其自身依赖链长度。
	EdgesLooked int
}

// CheckSession 在一份固定快照上复用记忆化表。
// Session 本身非并发安全；并发复核请各 goroutine 独立创建，
// 底层快照与裁决器保持只读，因此结论仍然完全一致。
type CheckSession struct {
	g    *depGraph
	dead map[RecordID]ReasonCode
	memo map[RecordID]*traverseResult
}

// NewCheckSession 为快照建立逐条复核会话（不修改快照）。
func (a *Adjudicator) NewCheckSession(snap *Snapshot) *CheckSession {
	return &CheckSession{
		g:    buildGraph(snap),
		dead: seedDeadSet(snap),
		memo: map[RecordID]*traverseResult{},
	}
}

// Check 对单条记录执行独立核对，返回判定与实际查看边数。
func (s *CheckSession) Check(id RecordID) RecordCheck {
	looked := 0
	r, _ := s.g.resolveWithMemo(id, s.dead, s.memo, &looked)
	return RecordCheck{
		ID:          id,
		Recoverable: r.ok,
		Reason:      r.code,
		EdgesLooked: looked,
	}
}
