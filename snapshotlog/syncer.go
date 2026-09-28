package snapshotlog

import (
	"sort"
	"sync"
)

// rangePhase 记录单个键范围的处理阶段。
type rangePhase int

const (
	phaseNone     rangePhase = iota // 尚未开始
	phaseSnapshot                   // 已记低水位，等待读快照/完成
	phaseDone                       // 已记高水位并完成修正，进入轮询
)

// rangeState 是一个键范围的处理位置与水位。
type rangeState struct {
	phase    rangePhase
	lowSeq   int64            // 低水位：开始前已提交的最大日志序号
	snapSeq  int64            // 读快照时源表返回的序号
	snapRows map[int64]string // 读快照瞬间冻结的范围内存活键
	highSeq  int64            // 高水位：完成修正时已提交的最大日志序号
	applied  int64            // 已应用到视图的最大日志序号（初始为 highSeq）
}

// Syncer 协调多个键范围的“快照 + 增量日志”衔接，维护一个下游视图。
// 所有方法并发安全；被拒绝的操作不会改变视图、处理位置或水位。
type Syncer struct {
	src    *Source
	cfg    config
	mu     sync.Mutex
	view   map[int64]string // 下游视图：键 -> 值
	ranges map[KeyRange]*rangeState
}

// NewSyncer 基于源表创建衔接组件。
func NewSyncer(src *Source, opts ...Option) *Syncer {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	return &Syncer{
		src:    src,
		cfg:    cfg,
		view:   make(map[int64]string),
		ranges: make(map[KeyRange]*rangeState),
	}
}

// overlapLocked 判断 r 是否与任一已登记范围（进行中或已完成）相交。
func (x *Syncer) overlapLocked(r KeyRange) bool {
	for ex := range x.ranges {
		if r.Overlaps(ex) {
			return true
		}
	}
	return false
}

// BeginSnapshot 第 1 步：记录键范围 r 的低水位。
// 拒绝原因：ErrInvalidRange / ErrRangeOverlap。
func (x *Syncer) BeginSnapshot(r KeyRange) (lowSeq int64, err error) {
	x.mu.Lock()
	defer x.mu.Unlock()

	if !r.Valid() {
		x.cfg.logger.Info("BeginSnapshot 拒绝",
			"input", r.String(), "reason", "invalid_range", "basis", "start > end")
		return 0, ErrInvalidRange
	}
	if x.overlapLocked(r) {
		x.cfg.logger.Info("BeginSnapshot 拒绝",
			"input", r.String(), "reason", "range_overlap",
			"basis", "与已登记范围(进行中或已完成)在闭区间边界上相交")
		return 0, ErrRangeOverlap
	}

	// 低水位 = 当前已提交的最大日志序号；与登记范围在同一把锁内完成。
	lowSeq = x.src.maxSeq()
	x.ranges[r] = &rangeState{phase: phaseSnapshot, lowSeq: lowSeq}
	x.cfg.logger.Info("BeginSnapshot 接受",
		"input", r.String(), "output_lowSeq", lowSeq,
		"basis", "范围合法且与已有范围互不相交")
	return lowSeq, nil
}

// ReadSnapshot 第 2 步：读取并冻结 r 的快照（不改变视图）。
// 可重复调用，返回第一次读取时冻结的同一份快照。
// 拒绝原因：ErrPhaseOrder（范围不存在或已完成）。
func (x *Syncer) ReadSnapshot(r KeyRange) (rows map[int64]string, snapSeq int64, err error) {
	x.mu.Lock()
	defer x.mu.Unlock()

	st := x.ranges[r]
	if st == nil {
		x.cfg.logger.Info("ReadSnapshot 拒绝",
			"input", r.String(), "reason", "phase_order",
			"basis", "范围不存在，尚未 BeginSnapshot")
		return nil, 0, ErrPhaseOrder
	}
	if st.phase != phaseSnapshot {
		x.cfg.logger.Info("ReadSnapshot 拒绝",
			"input", r.String(), "reason", "phase_order",
			"basis", "范围已完成，不能重复读快照", "phase", st.phase)
		return nil, 0, ErrPhaseOrder
	}

	// 首次读取时冻结快照；之后重复调用返回同一份冻结内容，
	// 保证 FinishSnapshot 修正的正是第 2 步读到的快照。
	if st.snapRows == nil {
		snap := x.src.snapshot(r)
		st.snapSeq = snap.seq
		st.snapRows = snap.rows
	}
	out := make(map[int64]string, len(st.snapRows))
	for k, v := range st.snapRows {
		out[k] = v
	}
	x.cfg.logger.Info("ReadSnapshot",
		"input", r.String(), "output_snapSeq", st.snapSeq,
		"output_rows", len(out),
		"basis", "返回第2步冻结的快照，不含低水位之后尚未修正的写入")
	return out, st.snapSeq, nil
}

// FinishSnapshot 第 3 步：记高水位，读取 (低水位, 高水位] 之间落在 r
// 内的日志并按序号修正冻结快照，把结果原子并入视图；范围进入轮询阶段。
// 拒绝原因：ErrPhaseOrder / ErrViewLimitExceeded。
// 被拒绝时视图、处理位置与水位均保持不变。
func (x *Syncer) FinishSnapshot(r KeyRange) (applied []AppliedEvent, err error) {
	x.mu.Lock()
	defer x.mu.Unlock()

	st := x.ranges[r]
	if st == nil {
		x.cfg.logger.Info("FinishSnapshot 拒绝",
			"input", r.String(), "reason", "phase_order",
			"basis", "范围不存在，尚未 BeginSnapshot")
		return nil, ErrPhaseOrder
	}
	if st.phase != phaseSnapshot {
		x.cfg.logger.Info("FinishSnapshot 拒绝",
			"input", r.String(), "reason", "phase_order",
			"basis", "快照已完成，不能重复进入完成阶段", "phase", st.phase)
		return nil, ErrPhaseOrder
	}
	if st.snapRows == nil {
		x.cfg.logger.Info("FinishSnapshot 拒绝",
			"input", r.String(), "reason", "phase_order",
			"basis", "未先 ReadSnapshot 就读取高水位")
		return nil, ErrPhaseOrder
	}

	// 高水位 = 当前已提交的最大日志序号。
	highSeq := x.src.maxSeq()

	// 从冻结快照出发，按序重放 (lowSeq, highSeq] 内落在 r 的日志。
	// 快照已含 <=snapSeq 的效果，重放其中 <=snapSeq 的部分是幂等的，
	// (snapSeq, highSeq] 则补上快照期间及之后的写入。
	corrected := make(map[int64]string, len(st.snapRows))
	for k, v := range st.snapRows {
		corrected[k] = v
	}
	replayed := x.src.entriesBetween(st.lowSeq, highSeq)
	events := make([]AppliedEvent, 0, len(replayed))
	for _, e := range replayed {
		if !r.Contains(e.Key) {
			continue
		}
		applyToMap(corrected, e)
		events = append(events, AppliedEvent{Entry: e})
	}

	// 行数上限校验：仅统计视图中尚不存在的新增存活键。
	// 范围互不相交，正常情况下全部为新增，这里仍做通用统计。
	if x.cfg.maxViewRows > 0 {
		added := 0
		for k := range corrected {
			if _, ok := x.view[k]; !ok {
				added++
			}
		}
		if len(x.view)+added > x.cfg.maxViewRows {
			x.cfg.logger.Info("FinishSnapshot 拒绝",
				"input", r.String(), "reason", "view_limit_exceeded",
				"basis", "并入后视图行数将超过上限",
				"current_rows", len(x.view), "new_rows", added,
				"limit", x.cfg.maxViewRows, "highSeq", highSeq)
			return nil, ErrViewLimitExceeded
		}
	}

	// 原子提交：并入视图、写水位与处理位置。
	for k, v := range corrected {
		x.view[k] = v
	}
	st.phase = phaseDone
	st.highSeq = highSeq
	st.applied = highSeq

	x.cfg.logger.Info("FinishSnapshot 接受",
		"input", r.String(), "lowSeq", st.lowSeq, "snapSeq", st.snapSeq,
		"output_highSeq", highSeq, "replayed_in_range", len(events),
		"view_rows", len(x.view),
		"basis", "快照+(低水位,高水位]范围内日志按序修正后原子并入视图")
	return events, nil
}

// pendingEntry 把一条待应用日志与其所属范围绑定。
type pendingEntry struct {
	e Entry
	r KeyRange
}

// Poll 轮询：把所有已完成范围内、序号严格大于各自已应用位置的日志
// 按全局序号顺序应用到视图，返回本次实际应用的事件。
// 只应用落在“已完成范围”内且序号高于该范围高水位的日志；
// 进行中的范围、不属于任何已完成范围的键一律跳过。
// 超限时整批拒绝：视图与各范围处理位置均不前进。
func (x *Syncer) Poll() ([]AppliedEvent, error) {
	x.mu.Lock()
	defer x.mu.Unlock()

	// 收集各已完成范围的待应用日志（append-only 日志读取不可变数据）。
	pending := make([]pendingEntry, 0)
	for r, st := range x.ranges {
		if st.phase != phaseDone {
			continue
		}
		for _, e := range x.src.entriesAfter(st.applied) {
			if r.Contains(e.Key) && e.Seq > st.applied {
				pending = append(pending, pendingEntry{e: e, r: r})
			}
		}
	}
	if len(pending) == 0 {
		x.cfg.logger.Info("Poll", "output_applied", 0,
			"basis", "没有高于各已完成范围处理位置的新日志")
		return nil, nil
	}

	// 按全局序号归并，保证跨范围也按日志真实顺序应用，输出确定。
	sort.Slice(pending, func(i, j int) bool { return pending[i].e.Seq < pending[j].e.Seq })

	// 在影子状态上模拟整批应用，只按最终行数判定上限，保证原子性。
	if x.cfg.maxViewRows > 0 {
		touched := make(map[int64]bool) // 仅记录本批涉及键的存在性
		count := len(x.view)
		present := func(k int64) bool {
			if v, ok := touched[k]; ok {
				return v
			}
			_, inView := x.view[k]
			return inView
		}
		for _, p := range pending {
			was := present(p.e.Key)
			switch p.e.Op {
			case OpPut:
				if !was {
					count++
				}
				touched[p.e.Key] = true
			case OpDelete:
				if was {
					count--
				}
				touched[p.e.Key] = false
			}
		}
		if count > x.cfg.maxViewRows {
			seqs := []int64{pending[0].e.Seq, pending[len(pending)-1].e.Seq}
			x.cfg.logger.Info("Poll 拒绝",
				"reason", "view_limit_exceeded",
				"basis", "整批应用后视图行数将超过上限，批次原子回滚",
				"batch_seq_range", seqs, "batch_size", len(pending),
				"projected_rows", count, "limit", x.cfg.maxViewRows)
			return nil, ErrViewLimitExceeded
		}
	}

	// 提交：逐条改视图，并推进各范围处理位置（每范围的序号是连续前缀）。
	events := make([]AppliedEvent, 0, len(pending))
	lastApplied := make(map[KeyRange]int64)
	for _, p := range pending {
		applyToMap(x.view, p.e)
		lastApplied[p.r] = p.e.Seq
		events = append(events, AppliedEvent{Entry: p.e})
	}
	for r, seq := range lastApplied {
		x.ranges[r].applied = seq
	}

	x.cfg.logger.Info("Poll",
		"output_applied", len(events),
		"first_seq", events[0].Entry.Seq, "last_seq", events[len(events)-1].Entry.Seq,
		"view_rows", len(x.view),
		"basis", "仅应用已完成范围内、序号高于处理位置的日志，按全局序号排序")
	return events, nil
}

// View 返回当前下游视图的副本（键 -> 值）。
func (x *Syncer) View() map[int64]string {
	x.mu.Lock()
	defer x.mu.Unlock()
	out := make(map[int64]string, len(x.view))
	for k, v := range x.view {
		out[k] = v
	}
	return out
}

// AppliedSeq 返回范围 r 已应用到视图的最大日志序号；范围不存在返回 0。
func (x *Syncer) AppliedSeq(r KeyRange) int64 {
	x.mu.Lock()
	defer x.mu.Unlock()
	if st := x.ranges[r]; st != nil {
		return st.applied
	}
	return 0
}

// maxSeq 返回源表当前已提交的最大日志序号（空表为 0）。
func (s *Source) maxSeq() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.next - 1
}

// applyToMap 把一条日志应用到给定的键值映射：PUT 覆盖，DELETE 删除。
func applyToMap(m map[int64]string, e Entry) {
	switch e.Op {
	case OpPut:
		m[e.Key] = e.Value
	case OpDelete:
		delete(m, e.Key)
	}
}
