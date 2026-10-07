package snapshot

import "fmt"

// Options 控制一次导出会话的资源配额。
type Options struct {
	// MaxSnapshotEntries 快照物化条目数上限（对象+链接+动作），
	// 0 表示不限。超出时报 ErrResourceExhausted。
	MaxSnapshotEntries int
	// MaxIncrements 本会话允许输出的增量记录条数上限，0 表示不限。
	MaxIncrements int
}

// Coordinator 是导出协同组件的编排者：串联边界确定、归属判定与
// 完整性核对，对外提供快照读取与增量流。
type Coordinator struct {
	journal *Journal
}

// NewCoordinator 构造协调器。
func NewCoordinator(j *Journal) *Coordinator {
	return &Coordinator{journal: j}
}

// BeginExport 开启一次导出会话：首先确定边界位点（优先级最高的
// 边界定义冲突在此判定），随后构造判定器与核对器。
func (c *Coordinator) BeginExport(req BoundaryRequest, opts Options) (*Session, error) {
	boundary, err := NewBoundaryDeterminer(c.journal).Resolve(req)
	if err != nil {
		return nil, err
	}
	return &Session{
		journal:    c.journal,
		boundary:   boundary,
		classifier: NewClassifier(boundary, nil),
		checker:    NewChecker(),
		opts:       opts,
		next:       boundary + 1,
	}, nil
}

// Session 是一次导出会话。快照与增量共用同一边界；会话可并发
// 存在多个，各自独立。
type Session struct {
	journal    *Journal
	boundary   LSN
	classifier *Classifier
	checker    *Checker
	opts       Options
	next       LSN          // 下一条待输出增量的起始 LSN
	emitted    int          // 已输出增量条数（资源计量）
	err        *ExportError // 非空表示会话已终止
}

// Boundary 返回本会话的边界位置。
func (s *Session) Boundary() LSN { return s.boundary }

// fail 记录会话的首个错误并终止会话：此后一切调用都返回该错误，
// 即“拒绝本次导出中尚未输出的部分”；已输出部分不受影响。
func (s *Session) fail(err *ExportError) *ExportError {
	if s.err == nil {
		s.err = err
	}
	return s.err
}

// Snapshot 物化边界位置的全量状态并做引用完整性核对。
// 快照内容恰好等于边界之前（含边界）全部被接受写入的最终结果：
// 它直接由接受日志重放得到，不依赖任何可能滞后的物化介质。
func (s *Session) Snapshot() (State, error) {
	if s.err != nil {
		return State{}, s.err
	}
	state := NewState()
	entries := s.journal.Entries(0, s.boundary)
	for _, w := range entries {
		state.Apply(w)
	}
	if err := s.checker.CheckSnapshot(state); err != nil {
		return State{}, s.fail(err)
	}
	// 资源检查排在逻辑检查之后：同一决策点上引用完整性冲突
	// （确定性逻辑错误）优先于资源不足（瞬态环境错误）被报告。
	if s.opts.MaxSnapshotEntries > 0 && len(entries) > s.opts.MaxSnapshotEntries {
		return State{}, s.fail(&ExportError{
			Kind:   ErrResourceExhausted,
			Rule:   RuleResourceLimit,
			Detail: fmt.Sprintf("snapshot needs %d entries, limit %d", len(entries), s.opts.MaxSnapshotEntries),
		})
	}
	return state, nil
}

// Next 返回下一条增量记录；若当前已追平日志尖端，返回 (nil, nil)。
// 每条增量记录恰好承载一笔完整事务；输出前依次做原子性（R4）、
// 引用完整性（R5）与资源（R6）检查，任一失败即拒绝本次导出中
// 尚未输出的部分，已输出部分保持有效。
func (s *Session) Next() (*Increment, error) {
	if s.err != nil {
		return nil, s.err
	}
	tip := s.journal.Tip()
	if s.next > tip {
		return nil, nil
	}
	// 取从 s.next 开始的连续同事务写入，聚成一条增量记录。
	// 归属判定：该记录全部写入的 LSN 均 > boundary（边界由 R3
	// 保证不切分事务），逐条经 classifier 复核。
	entries := s.journal.Entries(s.next-1, tip)
	if len(entries) == 0 {
		return nil, nil
	}
	txn := entries[0].Txn
	incr := Increment{Txn: txn, FromLSN: entries[0].LSN}
	for _, w := range entries {
		if w.Txn != txn {
			break
		}
		if s.classifier.Classify(w.LSN) != SideIncrement {
			return nil, s.fail(&ExportError{
				Kind:   ErrBoundaryConflict,
				Rule:   RuleBoundaryInclusive,
				Detail: fmt.Sprintf("write at LSN %d classified inside boundary %d", w.LSN, s.boundary),
				LSN:    w.LSN,
			})
		}
		incr.Writes = append(incr.Writes, w)
		incr.ToLSN = w.LSN
	}
	if err := s.checker.CheckIncrement(incr); err != nil {
		return nil, s.fail(err)
	}
	// 资源检查排在原子性与引用完整性之后（固定优先级）。
	if s.opts.MaxIncrements > 0 && s.emitted >= s.opts.MaxIncrements {
		return nil, s.fail(&ExportError{
			Kind:   ErrResourceExhausted,
			Rule:   RuleResourceLimit,
			Detail: fmt.Sprintf("increment limit %d reached", s.opts.MaxIncrements),
			LSN:    s.next,
		})
	}
	s.next = incr.ToLSN + 1
	s.emitted++
	return &incr, nil
}

// Drain 依次取出当前所有可用增量记录，直到追平尖端或出错。
// 出错时返回已成功输出的前缀与错误本身。
func (s *Session) Drain() ([]Increment, error) {
	var out []Increment
	for {
		incr, err := s.Next()
		if err != nil {
			return out, err
		}
		if incr == nil {
			return out, nil
		}
		out = append(out, *incr)
	}
}
