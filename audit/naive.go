package audit

// NaiveModel 是一个独立维护的“朴素预言机”：
//
// 它不使用任何快照或索引，每次重放都从序号 1 开始线性扫描
// 某类型的全部审计记录，逐条应用相同的记录语义
// （提交动作改状态、回退跳过、订正覆盖）。
//
// 它与 Replayer 的实现完全独立（不共享任何状态），专门用于在
// 大量随机序列下逐条对照，保证快速重放器与“显然正确”的线性
// 重放结果始终一致。
type NaiveModel struct {
	log *AuditLog
}

// NewNaiveModel 创建朴素模型。
func NewNaiveModel(log *AuditLog) *NaiveModel {
	return &NaiveModel{log: log}
}

// Replay 永远从头扫描 [1..toSeq] 的全部记录，再截取区间结果。
func (m *NaiveModel) Replay(typeName string, fromSeq, toSeq int64) (ReplayResult, error) {
	if fromSeq < 0 || toSeq < fromSeq {
		return ReplayResult{}, &IllegalRequestError{Reason: "invalid range"}
	}

	// 完整重放：朴素模型始终从序号 1 开始，用与快速重放器相同的
	// 记录语义（但绝不共享其快照/索引状态）前向推进一个状态帧。
	all := m.log.Range(typeName, 0, toSeq) // 0..toSeq ⇒ 记录 1..toSeq
	current := newFrame()

	var stateFrom map[string]string
	events := make([]ReplayEvent, 0)

	for _, rec := range all {
		diff := applyRecord(current, rec)
		if rec.Seq == fromSeq {
			stateFrom = current.state()
		}
		if rec.Seq > fromSeq && len(diff) > 0 {
			events = append(events, ReplayEvent{
				Seq:      rec.Seq,
				Kind:     rec.Kind,
				ActionID: rec.ActionID,
				Changes:  diff,
			})
		}
	}
	if fromSeq == 0 {
		stateFrom = map[string]string{}
	}
	if stateFrom == nil {
		// fromSeq 落在 toSeq 之后不可能发生；fromSeq==toSeq 时
		// 区间为空，两端状态相同。
		stateFrom = current.state()
	}

	return ReplayResult{
		FromSeq:   fromSeq,
		ToSeq:     toSeq,
		StateFrom: stateFrom,
		StateTo:   current.state(),
		Events:    events,
	}, nil
}
