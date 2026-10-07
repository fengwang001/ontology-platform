package ontology

import "fmt"

// Replayer 按审计序列重放、重建任意序号区间的状态变更。
//
// 订正具有追溯语义：一条订正一旦写入，重放时它覆盖所指动作对全部
// 历史时刻的影响。某实例在任意时刻的有效值恒等于「其最近一次有效提交
// 动作的写入值，再叠加该动作的最新订正（若有）」。订正最新值由
// AuditStore 增量维护、O(1) 点查得到，因此重放无需扫描订正历史。
//
// 复杂度：从不晚于起点的最近快照出发，只需读取
// SnapshotInterval + (hi-lo) 条记录（外加每个活跃实例一次 O(1) 点查），
// 与该对象类型历史审计记录总数无关。
type Replayer struct {
	store     *AuditStore
	scanCost  int
	pointLook int
	snapshots map[int]*Snapshot
}

// NewReplayer 创建重放器。
func NewReplayer(store *AuditStore) *Replayer {
	return &Replayer{store: store, snapshots: map[int]*Snapshot{}}
}

// ScanCost 返回最近一次范围重放实际读取的记录条数（复杂度可验证度量）。
func (rp *Replayer) ScanCost() int { return rp.scanCost }

// PointLookups 返回最近一次重放为订正点查付出的 O(1) 查询次数。
func (rp *Replayer) PointLookups() int { return rp.pointLook }

// effectiveAfter 返回动作记录 seq 中实例 inst 的（最新订正后）有效值。
func (rp *Replayer) effectiveAfter(seq int, c Change) string {
	rp.pointLook++
	if v, ok := rp.store.correctedAfter(seq, c.Instance); ok {
		return v
	}
	return c.After
}

// ensureSnapshots 增量构建所有已到达间隔边界的快照并缓存复用；
// 每次调用只读取上一边界之后的新记录，与历史总量无关。
func (rp *Replayer) ensureSnapshots() {
	last := 0
	for b := range rp.snapshots {
		if b > last {
			last = b
		}
	}
	state := rp.store.initialCopy()
	latest := map[string]int{}
	if last > 0 {
		prev := rp.snapshots[last]
		state = cloneState(prev.state)
		for k, v := range prev.latest {
			latest[k] = v
		}
	}
	for _, rec := range rp.store.viewRange(last, rp.store.Len()) {
		rp.apply(rec, state, latest)
		if rec.Seq%SnapshotInterval == 0 {
			rp.snapshots[rec.Seq] = snapshotFrom(rec.Seq, state, latest)
		}
	}
}

// baselineSnapshot 返回不晚于 at 的最近间隔快照（否则序号 0 空快照）。
func (rp *Replayer) baselineSnapshot(at int) *Snapshot {
	rp.ensureSnapshots()
	base := (at / SnapshotInterval) * SnapshotInterval
	if snap, ok := rp.snapshots[base]; ok {
		return snap
	}
	return &Snapshot{at: 0, state: rp.store.initialCopy(), latest: map[string]int{}}
}

// apply 把一条记录应用到工作状态（追溯订正视图）。
func (rp *Replayer) apply(rec *Record, state map[string]string, latest map[string]int) {
	if rec.Kind == KindAction && rec.Outcome == Committed {
		for _, c := range rec.Changes {
			state[c.Instance] = rp.effectiveAfter(rec.Seq, c)
			latest[c.Instance] = rec.Seq
		}
	}
	// 回退记录不改状态；订正记录不改「每实例最新动作」，
	// 其影响通过 correctedAfter 点查在读取该动作值时体现。
}

// startState 由快照构建可写工作基线，并对「最新动作已在快照之后被订正」
// 的实例做点查校正（快照是边界时刻物化值，订正可能在之后写入）。
func (rp *Replayer) startState(snap *Snapshot) (map[string]string, map[string]int) {
	state := cloneState(snap.state)
	latest := map[string]int{}
	for k, v := range snap.latest {
		latest[k] = v
	}
	if snap.at > 0 {
		// 快照是边界时刻的物化值，之后可能又写入订正；按 latest 点查
		// 每个活跃实例最新动作的当前有效值，得到追溯订正后的基线。
		for inst, seq := range latest {
			raw, v, hasCorr := rp.store.effectiveValueAt(seq, inst)
			rp.pointLook++
			if hasCorr {
				state[inst] = v
			} else {
				state[inst] = raw
			}
		}
	}
	return state, latest
}

// ReplayRange 返回重放开区间 (lo, hi] 的有序事件。
func (rp *Replayer) ReplayRange(lo, hi int) ([]ChangeEvent, error) {
	if lo < 0 || hi < lo {
		return nil, fmt.Errorf("invalid range (%d,%d]", lo, hi)
	}
	total := rp.store.Len()
	if hi > total {
		return nil, fmt.Errorf("hi %d beyond sequence length %d", hi, total)
	}
	rp.scanCost = 0
	rp.pointLook = 0

	snap := rp.baselineSnapshot(lo)
	base := snap.at
	state, latest := rp.startState(snap)

	events := []ChangeEvent{}
	for _, rec := range rp.store.viewRange(base, hi) {
		rp.scanCost++
		if rec.Seq <= lo {
			rp.apply(rec, state, latest) // 把基线推进到 lo，不产出事件
			continue
		}
		switch rec.Kind {
		case KindAction:
			if rec.Outcome == RolledBack {
				events = append(events, ChangeEvent{Seq: rec.Seq, Kind: KindAction, Outcome: RolledBack})
				continue
			}
			for _, c := range rec.Changes {
				before := state[c.Instance]
				after := rp.effectiveAfter(rec.Seq, c)
				events = append(events, ChangeEvent{
					Seq:     rec.Seq,
					Kind:    KindAction,
					Outcome: Committed,
					Action:  &ActionChange{Instance: c.Instance, Before: before, After: after},
				})
			}
		case KindCorrection:
			events = append(events, ChangeEvent{Seq: rec.Seq, Kind: KindCorrection, Outcome: Committed})
		}
		rp.apply(rec, state, latest)
	}
	return events, nil
}

// StateAt 返回序号 r 处（追溯订正视图）全部实例状态。
func (rp *Replayer) StateAt(r int) map[string]string {
	rp.scanCost = 0
	rp.pointLook = 0
	snap := rp.baselineSnapshot(r)
	state, latest := rp.startState(snap)
	for _, rec := range rp.store.viewRange(snap.at, r) {
		rp.scanCost++
		rp.apply(rec, state, latest)
	}
	return state
}
