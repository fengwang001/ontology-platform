package bitemporal

// 仅测试期可见的内部访问辅助。

// ProbeReplay 携带复杂度探针执行回放。
func (s *Snapshot) ProbeReplay(linkType ID, recordTime, validTime int64, p *ProbeCounts) ReplayResult {
	return s.replay(linkType, recordTime, validTime, p)
}

// CurrentRuleGen 暴露当前规则版本代号。
func (st *Store) CurrentRuleGen() int64 {
	return st.CurrentSnapshot().ruleGen
}
