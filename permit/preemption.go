package permit

import "strings"

// PermitState 是供外部对照模型比较的规范许可状态。
type PermitState struct {
	Segment  string
	Lanes    int
	Priority Priority
	Start    Time
	End      Time
	Status   Status
	Order    int64
	History  []HistoryEntry
}

// Snapshot 返回全部许可的规范状态快照。
func (s *Service) Snapshot() map[string]PermitState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *Service) snapshotLocked() map[string]PermitState {
	out := map[string]PermitState{}
	for id, p := range s.permits {
		out[id] = PermitState{
			Segment: p.Segment, Lanes: p.Lanes, Priority: p.Priority,
			Start: p.Current.Start, End: p.Current.End, Status: p.Status,
			Order:   p.ApproveOrder,
			History: append([]HistoryEntry(nil), p.History...),
		}
	}
	return out
}

func (s *Service) newPermitFromApply(req ApplyRequest, seg *Segment) *Permit {
	return &Permit{
		ID:       req.ID,
		Segment:  req.Segment,
		Lanes:    req.Lanes,
		Priority: req.Priority,
		Current:  Interval{Start: req.Start, End: req.End},
		Corridor: seg.Corridor,
	}
}

// ghostPermit 构造审查用候选（不写入 permits 表）。
func (s *Service) ghostPermit(p *Permit, iv Interval) *Permit {
	return &Permit{
		ID:       p.ID,
		Segment:  p.Segment,
		Lanes:    p.Lanes,
		Priority: p.Priority,
		Current:  iv,
		Corridor: p.Corridor,
	}
}

func (s *Service) advanceClock(t Time) {
	s.clock = t
	s.clockSet = true
}

func (s *Service) recordOp(kind OperationKind, at Time, note string,
	a *ApplyRequest, e *ExtendRequest, r *RevokeRequest) {
	s.operations = append(s.operations, OperationRecord{
		Seq:  int64(len(s.operations)) + 1,
		Kind: kind, At: at,
		Apply: a, Extend: e, Revoke: r, Note: note,
	})
}

// —— 索引维护 ——
// treap 全量保留节点；已结束节点保留以供任意历史时刻查询（枚举被 maxEnd 剪枝），
// 撤销与时段重定位通过墓碑（valid=false）表达。

// insertPermit 把许可当前时段插入路段树（常规许可另入走廊树）。
func (s *Service) insertPermit(p *Permit) {
	p.indexedStart = p.Current.Start
	node := newIndexNode(p)
	s.segIndex[p.Segment].upsert(node)
	if p.Priority == Regular {
		s.ensureCorridor(p.Corridor)
		s.corrIndex[p.Corridor].upsert(newIndexNode(p))
	}
}

// deactivatePermit 把许可当前时段对应节点置为墓碑。
func (s *Service) deactivatePermit(p *Permit) {
	s.segIndex[p.Segment].invalidate(p.indexedStart, p.ID)
	if p.Priority == Regular {
		if ix := s.corrIndex[p.Corridor]; ix != nil {
			ix.invalidate(p.indexedStart, p.ID)
		}
	}
}

func (s *Service) ensureCorridor(c string) {
	if _, ok := s.corrIndex[c]; !ok {
		s.corrIndex[c] = &activeIndex{}
	}
}

// executeEmergency 在应急许可 cand 已入索引后，对抢占目标执行抢占与顺延重审。
func (s *Service) executeEmergency(cand *Permit, targets []*Permit, at Time) (
	preempted []string, reschedule map[string]bool, ivs map[string]Interval, note string) {
	note = s.runPreemption(cand, targets, at)
	return collectResults(targets, note)
}

// emergencyExtendTargets 求应急许可 p 延至 newEnd 时新增需抢占的常规许可。
func (s *Service) emergencyExtendTargets(p *Permit, newEnd Time) []*Permit {
	ghost := s.ghostPermit(p, Interval{Start: p.Current.Start, End: newEnd})
	return s.examiner().preemptTargets(ghost)
}

// runPreemption 对目标集合执行：标记墓碑、切出顺延候选、按原批准次序依次重审。
func (s *Service) runPreemption(em *Permit, targets []*Permit, at Time) string {
	ids := make([]string, 0, len(targets))
	for _, p := range targets {
		ids = append(ids, p.ID)
		p.PreemptedBy = em.ID

		// 抢占的第一时间从活跃索引摘除（墓碑），随后切换到顺延候选时段。
		s.deactivatePermit(p)

		var shifted Interval
		switch {
		case p.Current.Start >= at:
			shifted = Interval{Start: em.Current.End, End: em.Current.End + (p.Current.End - p.Current.Start)}
		case p.Current.End > at:
			shifted = Interval{Start: em.Current.End, End: em.Current.End + (p.Current.End - at)}
		default:
			shifted = p.Current
		}
		p.Current = shifted
		p.Status = StatusPreempted
		p.History = append(p.History, HistoryEntry{
			At: at, Kind: "PREEMPT", Priority: p.Priority,
			Interval: shifted, By: em.ID,
			Detail: "preempted by emergency " + em.ID + "; deferred candidate after emergency ends",
		})
	}

	// 按原批准时刻先后（ApproveOrder 升序）依次重审，先处理者影响后处理者。
	sortPermitsAscOrder(targets)
	rejected := make([]string, 0)
	ex := s.examiner()
	ex.liveAfter = at
	for _, p := range targets {
		err := ex.checkRegular(p, "")
		if err != nil {
			p.Status = StatusPendingReschedule
			p.History = append(p.History, HistoryEntry{
				At: at, Kind: "RESCHEDULE_REJECTED", Priority: p.Priority,
				Interval: p.Current, By: em.ID,
				Detail: "deferred re-review failed: " + err.Error(),
			})
			rejected = append(rejected, p.ID)
			continue
		}
		p.Status = StatusApproved
		p.Original = p.Current
		s.insertPermit(p)
		p.History = append(p.History, HistoryEntry{
			At: at, Kind: "RESCHEDULE_APPROVED", Priority: p.Priority,
			Interval: p.Current, By: em.ID,
			Detail: "deferred re-review approved with new interval",
		})
	}

	note := "accepted; preempted=[" + strings.Join(ids, ",") + "]"
	if len(rejected) > 0 {
		note += " reschedule_rejected=[" + strings.Join(rejected, ",") + "]"
	}
	return note
}

func collectResults(targets []*Permit, note string) (
	preempted []string, reschedule map[string]bool, ivs map[string]Interval, _ string) {
	reschedule = map[string]bool{}
	ivs = map[string]Interval{}
	for _, p := range targets {
		preempted = append(preempted, p.ID)
		reschedule[p.ID] = p.Status == StatusApproved
		ivs[p.ID] = p.Current
	}
	return preempted, reschedule, ivs, note
}
