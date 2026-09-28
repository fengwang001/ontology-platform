package snapshot

import (
	"fmt"
	"log/slog"
	"sort"
	"sync"
)

// RangePhase 标识一个键范围当前所处的处理阶段。
type RangePhase int

const (
	PhaseBegun       RangePhase = iota // 已记低水位，尚未读快照
	PhaseSnapshotted                   // 已读快照，尚未记高水位并修正
	PhaseCompleted                     // 已完成并安装到视图
)

func (p RangePhase) String() string {
	switch p {
	case PhaseBegun:
		return "begun"
	case PhaseSnapshotted:
		return "snapshotted"
	case PhaseCompleted:
		return "completed"
	default:
		return "unknown"
	}
}

// RangeInfo 是一个范围的对外可观测状态。
type RangeInfo struct {
	Start    Key
	End      Key
	Phase    RangePhase
	LowSeq   int64 // 低水位
	HighSeq  int64 // 高水位（完成后即该范围的轮询处理位置）
	RowCount int   // 完成时安装到视图中的行数
}

// rangeState 是一个键范围的内部状态。
type rangeState struct {
	start, end Key
	phase      RangePhase
	lowSeq     int64         // 低水位：BeginRange 时刻的源表最后序号
	highSeq    int64         // 高水位：CompleteRange 时刻的源表最后序号；完成后兼作轮询处理位置
	snapshot   []KV          // 阶段二读到的一致快照
	rows       map[Key]Value // 该范围在视图中的行（仅完成后有效）
	rowCount   int
}

// Syncer 衔接快照与增量日志，维护与源表最终一致的下游视图。
//
// 每个键范围固定按三步推进：BeginRange（记低水位）→ SnapshotRange（读快照）
// → CompleteRange（记高水位并按序用 (低,高] 之间落在范围内的日志修正快照）。
// 之后 Poll 只把各范围处理位置（高水位）之后、且落在已完成范围内的日志
// 应用到视图并推进处理位置。
//
// 所有状态变更都在 mu 串行下完成；被拒绝的操作在校验阶段返回错误，
// 不会触及视图、处理位置或水位。
type Syncer struct {
	mu      sync.Mutex
	src     *Source
	ranges  map[Key]*rangeState // 以起始键索引
	log     *slog.Logger
	maxRows int // 视图行数上限；<=0 表示不限
}

// Option 配置 Syncer。
type Option func(*Syncer)

// WithLogger 注入日志记录器；每个操作都会打印输入、输出与判定依据。
func WithLogger(l *slog.Logger) Option {
	return func(s *Syncer) { s.log = l }
}

// WithMaxViewRows 限制视图最大行数；默认不限。
func WithMaxViewRows(n int) Option {
	return func(s *Syncer) { s.maxRows = n }
}

// NewSyncer 创建衔接器。
func NewSyncer(src *Source, opts ...Option) *Syncer {
	s := &Syncer{
		src:     src,
		ranges:  make(map[Key]*rangeState),
		log:     slog.Default(),
		maxRows: 0,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// closedIntervalsOverlap 判断两个闭区间是否相交（含端点相接）。
func closedIntervalsOverlap(aStart, aEnd, bStart, bEnd Key) bool {
	return aStart <= bEnd && bStart <= aEnd
}

// validateNewRange 校验范围合法性与不相交性。调用方须持有 mu。
func (s *Syncer) validateNewRange(start, end Key) error {
	if start > end {
		return fmt.Errorf("%w: start=%d end=%d", ErrInvalidRange, start, end)
	}
	for _, r := range s.ranges {
		if closedIntervalsOverlap(start, end, r.start, r.end) {
			return fmt.Errorf("%w: [%d,%d] intersects existing [%d,%d]",
				ErrRangeOverlap, start, end, r.start, r.end)
		}
	}
	return nil
}

// BeginRange 阶段一：校验范围并记录低水位（源表当前最后序号）。
// 返回低水位。拒绝（非法范围/相交）时不改变任何状态。
func (s *Syncer) BeginRange(start, end Key) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.validateNewRange(start, end); err != nil {
		s.log.Info("BeginRange rejected",
			"input", fmt.Sprintf("[%d,%d]", start, end), "reason", err.Error())
		return 0, err
	}
	low := s.src.LastSeq()
	s.ranges[start] = &rangeState{
		start: start, end: end, phase: PhaseBegun, lowSeq: low,
	}
	s.log.Info("BeginRange accepted",
		"input", fmt.Sprintf("[%d,%d]", start, end),
		"output", fmt.Sprintf("lowSeq=%d", low),
		"basis", "range valid and disjoint; lowSeq=source lastSeq")
	return low, nil
}

// SnapshotRange 阶段二：读取该范围的一致快照。
// 必须先 BeginRange，且同一范围不能重复快照。拒绝时不改变任何状态。
func (s *Syncer) SnapshotRange(start, end Key) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.ranges[start]
	if !ok || r.end != end {
		err := fmt.Errorf("%w: range [%d,%d] not begun", ErrPhaseOrder, start, end)
		s.log.Info("SnapshotRange rejected",
			"input", fmt.Sprintf("[%d,%d]", start, end), "reason", err.Error())
		return err
	}
	if r.phase != PhaseBegun {
		err := fmt.Errorf("%w: range [%d,%d] already in phase %s, want begun",
			ErrPhaseOrder, start, end, r.phase)
		s.log.Info("SnapshotRange rejected",
			"input", fmt.Sprintf("[%d,%d]", start, end), "reason", err.Error())
		return err
	}

	r.snapshot = s.src.Snapshot(start, end)
	r.phase = PhaseSnapshotted
	s.log.Info("SnapshotRange accepted",
		"input", fmt.Sprintf("[%d,%d]", start, end),
		"output", fmt.Sprintf("rows=%d", len(r.snapshot)),
		"basis", "consistent source snapshot under source read lock")
	return nil
}

// applyEntry 把一条日志按其 Op 应用到行集合，返回新增行数（删除/更新为 0 或负）。
func applyEntry(rows map[Key]Value, e Entry) int {
	switch e.Op {
	case OpPut:
		if _, exists := rows[e.Key]; !exists {
			rows[e.Key] = e.Value
			return 1
		}
		rows[e.Key] = e.Value
		return 0
	case OpDelete:
		if _, exists := rows[e.Key]; exists {
			delete(rows, e.Key)
			return -1
		}
		return 0
	default:
		return 0
	}
}

// CompleteRange 阶段三：记高水位，按序应用 (低水位, 高水位] 内且落在范围中的
// 日志以修正快照，再把结果原子安装到视图。返回高水位。
//
// 拒绝（阶段顺序错误、视图行数超限）时不安装任何行，也不记录高水位：
// 范围仍停留在 snapshotted 阶段，可修正后重试。
func (s *Syncer) CompleteRange(start, end Key) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.ranges[start]
	if !ok || r.end != end {
		err := fmt.Errorf("%w: range [%d,%d] not begun", ErrPhaseOrder, start, end)
		s.log.Info("CompleteRange rejected",
			"input", fmt.Sprintf("[%d,%d]", start, end), "reason", err.Error())
		return 0, err
	}
	if r.phase != PhaseSnapshotted {
		err := fmt.Errorf("%w: range [%d,%d] in phase %s, want snapshotted",
			ErrPhaseOrder, start, end, r.phase)
		s.log.Info("CompleteRange rejected",
			"input", fmt.Sprintf("[%d,%d]", start, end), "reason", err.Error())
		return 0, err
	}

	// 在临时集合上修正快照；只有全部校验通过后才提交，保证失败无副作用。
	rows := make(map[Key]Value, len(r.snapshot))
	for _, kv := range r.snapshot {
		rows[kv.Key] = kv.Value
	}
	high, entries := s.src.ReadLog(r.lowSeq, start, end)
	appliedFrom := r.lowSeq
	for _, e := range entries {
		applyEntry(rows, e)
		appliedFrom = e.Seq
	}

	// 行数上限校验：统计安装后视图的总行数（其他范围行数不变）。
	if s.maxRows > 0 {
		total := len(rows)
		for k, other := range s.ranges {
			if k != start && other.phase == PhaseCompleted {
				total += other.rowCount
			}
		}
		if total > s.maxRows {
			err := fmt.Errorf("%w: view rows would be %d, limit %d",
				ErrViewLimitExceeded, total, s.maxRows)
			s.log.Info("CompleteRange rejected",
				"input", fmt.Sprintf("[%d,%d]", start, end),
				"basis", fmt.Sprintf("lowSeq=%d highSeq=%d correctedRows=%d",
					r.lowSeq, high, len(rows)),
				"reason", err.Error())
			return 0, err
		}
	}

	// 提交：安装修正后的行、记高水位、推进阶段。
	r.rows = rows
	r.rowCount = len(rows)
	r.highSeq = high
	r.phase = PhaseCompleted
	s.log.Info("CompleteRange accepted",
		"input", fmt.Sprintf("[%d,%d]", start, end),
		"output", fmt.Sprintf("highSeq=%d rows=%d", high, len(rows)),
		"basis", fmt.Sprintf("lowSeq=%d; applied %d log entries in seq order up to highSeq",
			r.lowSeq, len(entries)),
		"lastAppliedSeq", appliedFrom)
	return high, nil
}

// Poll 轮询：对每个已完成范围，按序把处理位置（高水位）之后且落在该范围的
// 日志应用到视图，并把处理位置推进到本次读到的源表最后序号。
//
// 所有范围的修改先在各范围的临时副本上计算并统一做行数上限校验，
// 任一范围超限则整批拒绝：视图与全部处理位置保持不变。
// 返回本次实际应用的日志条数。
func (s *Syncer) Poll() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	type pending struct {
		r       *rangeState
		rows    map[Key]Value
		high    int64
		entries []Entry
	}

	pends := make([]*pending, 0)
	totalApplied := 0
	// 按起始键升序处理，保证输出对相同输入序列确定可复现。
	starts := make([]Key, 0, len(s.ranges))
	for k, r := range s.ranges {
		if r.phase == PhaseCompleted {
			starts = append(starts, k)
		}
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })

	for _, k := range starts {
		r := s.ranges[k]
		high, entries := s.src.ReadLog(r.highSeq, r.start, r.end)
		if high == r.highSeq {
			// 源表没有新日志：处理位置不动。
			continue
		}
		// high > r.highSeq 时即使没有落在范围内的条目也登记推进：
		// 范围固定，区间外的条目以后也不会与本范围相关。
		rows := make(map[Key]Value, r.rowCount)
		for k2, v := range r.rows {
			rows[k2] = v
		}
		for _, e := range entries {
			applyEntry(rows, e)
		}
		pends = append(pends, &pending{r: r, rows: rows, high: high, entries: entries})
		totalApplied += len(entries)
	}

	if len(pends) == 0 {
		s.log.Info("Poll", "output", "applied=0", "basis", "no new entries past high watermarks")
		return 0, nil
	}

	// 统一行数上限校验。
	if s.maxRows > 0 {
		total := 0
		pendingByRange := map[Key]bool{}
		for _, p := range pends {
			total += len(p.rows)
			pendingByRange[p.r.start] = true
		}
		for k, r := range s.ranges {
			if r.phase == PhaseCompleted && !pendingByRange[k] {
				total += r.rowCount
			}
		}
		if total > s.maxRows {
			err := fmt.Errorf("%w: view rows would be %d, limit %d",
				ErrViewLimitExceeded, total, s.maxRows)
			s.log.Info("Poll rejected",
				"basis", fmt.Sprintf("pendingRanges=%d appliedWouldBe=%d", len(pends), totalApplied),
				"reason", err.Error())
			return 0, err
		}
	}

	// 整批提交。
	for _, p := range pends {
		p.r.rows = p.rows
		p.r.rowCount = len(p.rows)
		p.r.highSeq = p.high
	}
	s.log.Info("Poll accepted",
		"output", fmt.Sprintf("applied=%d ranges=%d", totalApplied, len(pends)),
		"basis", "entries past each range highSeq, key within completed range, applied in seq order")
	return totalApplied, nil
}

// View 返回视图当前全部键值行的拷贝，按键升序。
func (s *Syncer) View() []KV {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]KV, 0)
	for _, r := range s.ranges {
		if r.phase != PhaseCompleted {
			continue
		}
		for k, v := range r.rows {
			out = append(out, KV{Key: k, Value: v})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Ranges 返回全部范围（进行中与已完成）的状态，按起始键升序。
func (s *Syncer) Ranges() []RangeInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	starts := make([]Key, 0, len(s.ranges))
	for k := range s.ranges {
		starts = append(starts, k)
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })
	out := make([]RangeInfo, 0, len(starts))
	for _, k := range starts {
		r := s.ranges[k]
		out = append(out, RangeInfo{
			Start: r.start, End: r.end, Phase: r.phase,
			LowSeq: r.lowSeq, HighSeq: r.highSeq, RowCount: r.rowCount,
		})
	}
	return out
}
