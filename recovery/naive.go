package recovery

// naiveEntry 是朴素模型中单个对象的已知状态链。
type naiveEntry struct {
	exists    bool
	state     State
	source    Source
	anchorSeq int
}

// NaiveReplay 是与生产实现完全独立的朴素重放参考模型：
// 对每条动作都从头线性扫描判断，不维护任何索引/投影，
// 仅作为随机构造输入下的对照（differential testing）。
type NaiveReplay struct {
	baseVersion Version
	known       map[ObjectID]naiveEntry
	covered     map[ObjectID]bool
	unreadable  map[ObjectID]string
	judgments   []ActionJudgment
}

// NewNaiveReplay 从与协调器相同的解码产物构造朴素模型。
func NewNaiveReplay(snap Snapshot, corrupt map[ObjectID]string,
	logStart Version, actions []Action, corruptErrs []error) (*NaiveReplay, error) {
	if !logStart.Equals(snap.BaseVersion) {
		return nil, &Error{Kind: KindVersionMismatch,
			Message: "naive: version mismatch"}
	}
	n := &NaiveReplay{
		baseVersion: snap.BaseVersion,
		known:       map[ObjectID]naiveEntry{},
		covered:     map[ObjectID]bool{},
		unreadable:  map[ObjectID]string{},
	}
	for id, why := range corrupt {
		n.unreadable[id] = why
		n.covered[id] = true
	}
	for _, obj := range snap.Objects {
		n.covered[obj.ID] = true
		n.known[obj.ID] = naiveEntry{
			exists: obj.Exists, state: obj.State,
			source: SourceSnapshot, anchorSeq: -1,
		}
	}
	for i := range actions {
		if corruptErrs[i] != nil {
			n.judgments = append(n.judgments, ActionJudgment{
				Index: i, Outcome: ActionCorrupt,
				Reason: corruptErrs[i].Error(),
			})
			continue
		}
		n.apply(i, actions[i])
	}
	return n, nil
}

func (n *NaiveReplay) apply(index int, a Action) {
	// 完整记录声明的对象先进入覆盖范围（与协调器一致）。
	for id := range a.Effects {
		n.covered[id] = true
	}
	blocked := false
	var blockers []ObjectID
	for id, eff := range a.Effects {
		if _, ok := n.known[id]; !ok && eff.Start == nil {
			blocked = true
			blockers = append(blockers, id)
		}
	}
	if blocked {
		n.judgments = append(n.judgments, ActionJudgment{
			Index: index, Seq: a.Seq, Outcome: ActionBlocked,
			BlockingObjects: blockers,
		})
		return
	}
	for id, eff := range a.Effects {
		prev, ok := n.known[id]
		if !ok {
			n.known[id] = naiveEntry{
				exists:    true,
				state:     Apply(*eff.Start, eff.Change),
				source:    SourceRebuilt,
				anchorSeq: a.Seq,
			}
			continue
		}
		prev.state = Apply(prev.state, eff.Change)
		prev.exists = true
		n.known[id] = prev
	}
	n.judgments = append(n.judgments, ActionJudgment{
		Index: index, Seq: a.Seq, Outcome: ActionApplied,
	})
}

// Report 返回朴素模型对某对象的对象级报告。
func (n *NaiveReplay) Report(id ObjectID) (ObjectReport, bool) {
	if !n.covered[id] {
		return ObjectReport{}, false
	}
	r := ObjectReport{Object: id, AnchorSeq: -1,
		SnapshotUnreadable: n.unreadable[id] != ""}
	if e, ok := n.known[id]; ok {
		r.Exists, r.State, r.Source, r.AnchorSeq =
			e.exists, e.state, e.source, e.anchorSeq
	} else {
		r.Source = SourceUnknown
	}
	return r, true
}

// Judgments 返回朴素模型的全部判定（调用方只读）。
func (n *NaiveReplay) Judgments() []ActionJudgment { return n.judgments }
