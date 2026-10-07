package ontology

// NaiveModel 独立维护全部状态变更历史。
//
// 它刻意「最笨」：不使用任何快照或点查索引，每次查询都从序号 1 开始
// 线性扫描全部记录。第一遍建立全局订正覆盖（最新订正胜出），第二遍
// 重放动作得到状态 / 区间事件。作为黄金模型与优化后的 Replayer 逐条对照。
type NaiveModel struct{ store *AuditStore }

// NewNaiveModel 基于审计序列创建朴素模型。
func NewNaiveModel(store *AuditStore) *NaiveModel { return &NaiveModel{store: store} }

// buildOverrides 从头线性扫描全部记录，建立 targetSeq -> instance -> 最新订正值。
// 订正具有追溯语义，故无论查询时刻为何，始终采纳序列中全部已写入订正。
func (m *NaiveModel) buildOverrides() map[int]map[string]string {
	overrides := map[int]map[string]string{}
	for _, rec := range m.store.viewAll() {
		if rec.Kind != KindCorrection {
			continue
		}
		ov, ok := overrides[rec.CorrectionOf]
		if !ok {
			ov = map[string]string{}
			overrides[rec.CorrectionOf] = ov
		}
		for _, c := range rec.Changes {
			ov[c.Instance] = c.After // 扫描顺序即串行顺序，后写为最新
		}
	}
	return overrides
}

func (m *NaiveModel) eff(overrides map[int]map[string]string, seq int, c Change) string {
	if ov, ok := overrides[seq][c.Instance]; ok {
		return ov
	}
	return c.After
}

// StateAt 从头重放到 r，返回追溯订正视图的状态。
func (m *NaiveModel) StateAt(r int) map[string]string {
	overrides := m.buildOverrides()
	state := m.store.initialCopy()
	for _, rec := range m.store.viewAll() {
		if rec.Seq > r {
			break
		}
		if rec.Kind == KindAction && rec.Outcome == Committed {
			for _, c := range rec.Changes {
				state[c.Instance] = m.eff(overrides, rec.Seq, c)
			}
		}
	}
	return state
}

// ReplayRange 从头扫描全部记录，返回开区间 (lo, hi] 的有序事件。
func (m *NaiveModel) ReplayRange(lo, hi int) []ChangeEvent {
	overrides := m.buildOverrides() // 追溯订正：始终采纳全部订正
	state := m.store.initialCopy()
	events := []ChangeEvent{}
	for _, rec := range m.store.viewAll() {
		if rec.Seq > hi {
			break
		}
		if rec.Kind == KindAction {
			if rec.Outcome == RolledBack {
				if rec.Seq > lo {
					events = append(events, ChangeEvent{Seq: rec.Seq, Kind: KindAction, Outcome: RolledBack})
				}
				continue
			}
			if rec.Seq > lo {
				for _, c := range rec.Changes {
					events = append(events, ChangeEvent{
						Seq:     rec.Seq,
						Kind:    KindAction,
						Outcome: Committed,
						Action:  &ActionChange{Instance: c.Instance, Before: state[c.Instance], After: m.eff(overrides, rec.Seq, c)},
					})
				}
			}
			for _, c := range rec.Changes {
				state[c.Instance] = m.eff(overrides, rec.Seq, c)
			}
		} else if rec.Kind == KindCorrection && rec.Seq > lo {
			events = append(events, ChangeEvent{Seq: rec.Seq, Kind: KindCorrection, Outcome: Committed})
		}
	}
	return events
}
