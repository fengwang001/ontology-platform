package ontology

import "sync"

// Segment 标识一条被导出记录所处的段。
type Segment int

const (
	// SegmentNone 尚未发出任何记录。
	SegmentNone Segment = iota
	// SegmentSnapshot 快照段：导出开始时刻已存在的记录（序号 <= 起点）。
	SegmentSnapshot
	// SegmentIncremental 增量段：导出开始之后才出现的记录（序号 > 起点）。
	SegmentIncremental
)

func (s Segment) String() string {
	switch s {
	case SegmentSnapshot:
		return "snapshot"
	case SegmentIncremental:
		return "incremental"
	default:
		return "none"
	}
}

// Range 是一个段实际覆盖的闭区间序号 [From, To]；空段时 To < From（0..-1）。
type Range struct {
	From int64
	To   int64
}

// Empty 报告该段未发出任何记录。
func (r Range) Empty() bool { return r.To < r.From }

// Report 是导出结束时报告的两段区间及核验结论。
type Report struct {
	Snapshot    Range
	Incremental Range
	// Split 是快照/增量分界：快照含 seq <= Split，增量含 seq > Split。
	Split int64
}

// VerifySeam 核验两段无缝：
// 快照段必须为 1..Split 完整前缀；增量段必须紧接其后连续无空洞。
func (r Report) VerifySeam() error {
	if r.Snapshot.Empty() {
		if r.Split != 0 {
			return ErrSeam
		}
	} else if r.Snapshot.From != 1 || r.Snapshot.To != r.Split {
		return ErrSeam
	}
	if r.Incremental.Empty() {
		return nil
	}
	if r.Incremental.From != r.Split+1 {
		return ErrSeam
	}
	return nil
}

// sessionState 为导出会话的生命周期状态。
type sessionState int

const (
	stateCreated sessionState = iota
	stateStarted
	stateEnded
)

// ExportSession 是一次独立的边导出边增量读取过程。
// 多个会话互不影响，且都从序号 1 开始顺序读到同一末端时，结果逐字段相同。
type ExportSession struct {
	log *Log

	mu      sync.Mutex
	state   sessionState
	split   int64 // Start 时记录的起点序号
	nextSeq int64 // 下一条待发序号

	snapFrom int64 // 快照段实际起点（首条发出序号）
	snapTo   int64 // 快照段实际末端
	incFrom  int64 // 增量段实际起点
	incTo    int64 // 增量段实际末端
}

// NewExport 在给定日志上创建一个尚未开始的导出会话。
func NewExport(l *Log) *ExportSession {
	return &ExportSession{
		log:      l,
		nextSeq:  1,
		snapFrom: 1,
		snapTo:   0,
		incFrom:  1,
		incTo:    0,
	}
}

// Start 记录起点序号 split：此刻日志中 seq <= split 的记录归快照段，
// 之后出现的 seq > split 的记录归增量段（段归属按序号而非到达先后）。
// 未结束会话数超限时整体拒绝，不登记、不改变任何状态。
func (s *ExportSession) Start() (split int64, err error) {
	s.mu.Lock()
	switch s.state {
	case stateStarted:
		s.mu.Unlock()
		return 0, ErrExportAlreadyStarted
	case stateEnded:
		s.mu.Unlock()
		return 0, ErrExportEnded
	}
	s.mu.Unlock()

	split, err = s.log.startExport(s)
	if err != nil {
		return 0, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// startExport 成功后状态不可能已被他人改变（会话由调用方独占驱动），
	// 仍按状态机写回，保持失败不改变已发序列的原子语义。
	if s.state != stateCreated {
		s.log.finishExport(s)
		return 0, ErrExportAlreadyStarted
	}
	s.state = stateStarted
	s.split = split
	return split, nil
}

// Split 返回 Start 时确定的快照/增量分界序号；未开始返回 0。
func (s *ExportSession) Split() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.split
}

// Next 按序号升序发出下一条记录：
// 下一条待发序号对应的记录尚不存在时返回 ok=false（不等待、不跳读）；
// 快照段从 1 发到 split，随后增量段严格按 split+1、split+2... 发出。
func (s *ExportSession) Next() (rec Record, seg Segment, ok bool, err error) {
	s.mu.Lock()
	if s.state == stateCreated {
		s.mu.Unlock()
		return Record{}, SegmentNone, false, ErrExportNotStarted
	}
	if s.state == stateEnded {
		s.mu.Unlock()
		return Record{}, SegmentNone, false, ErrExportEnded
	}
	seq := s.nextSeq
	split := s.split
	s.mu.Unlock()

	rec, exists := s.log.At(seq)
	if !exists {
		// 序号连续分配且序号小的记录必先存在，因此不存在只可能是“还没写到”。
		return Record{}, SegmentNone, false, nil
	}

	seg = SegmentIncremental
	if seq <= split {
		seg = SegmentSnapshot
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// 记录区间只增不改；加锁后重判状态与待发序号，保证并发下判定依据一致。
	if s.state != stateStarted || s.nextSeq != seq {
		return Record{}, SegmentNone, false, nil
	}
	switch seg {
	case SegmentSnapshot:
		if s.snapTo == 0 {
			s.snapFrom = seq
		}
		s.snapTo = seq
	default:
		if s.incTo == 0 {
			s.incFrom = seq
		}
		s.incTo = seq
	}
	s.nextSeq = seq + 1
	return rec, seg, true, nil
}

// Drained 报告下一条待发序号是否已超出日志当前末端（暂时无记录可取）。
func (s *ExportSession) Drained() (bool, error) {
	s.mu.Lock()
	if s.state == stateCreated {
		s.mu.Unlock()
		return false, ErrExportNotStarted
	}
	if s.state == stateEnded {
		s.mu.Unlock()
		return false, ErrExportEnded
	}
	next := s.nextSeq
	s.mu.Unlock()
	return next > s.log.LastSeq(), nil
}

// Emitted 返回本会话已发出的记录条数。
func (s *ExportSession) Emitted() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nextSeq - 1
}

// Report 返回两段实际区间，不改变会话状态。
func (s *ExportSession) Report() (Report, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == stateCreated {
		return Report{}, ErrExportNotStarted
	}
	return s.reportLocked(), nil
}

func (s *ExportSession) reportLocked() Report {
	return Report{
		Snapshot:    Range{From: s.snapFrom, To: s.snapTo},
		Incremental: Range{From: s.incFrom, To: s.incTo},
		Split:       s.split,
	}
}

// End 结束导出：注销会话、报告两段区间并核验无缝拼接。
// 若日志仍开放写入，则无法保证“完整导出”，返回 ErrLogStillOpen；
// 区间不满足快照为 1..split、增量紧接 split 连续时返回 ErrSeam。
// 两种错误下 Report 仍按实际发出区间给出；结束操作本身不回滚。
func (s *ExportSession) End() (Report, error) {
	s.mu.Lock()
	switch s.state {
	case stateCreated:
		s.mu.Unlock()
		return Report{}, ErrExportNotStarted
	case stateEnded:
		s.mu.Unlock()
		return Report{}, ErrExportEnded
	}
	report := s.reportLocked()
	s.state = stateEnded
	s.mu.Unlock()

	s.log.finishExport(s)

	if err := report.VerifySeam(); err != nil {
		return report, err
	}
	if !s.log.IsSealed() {
		return report, ErrLogStillOpen
	}
	if s.log.LastSeq() != report.Incremental.To &&
		!(report.Incremental.Empty() && s.log.LastSeq() == report.Split) {
		return report, ErrSeam
	}
	return report, nil
}
