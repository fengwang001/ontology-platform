package ontology

import "sync"

// TopNTracker 在撤回式变更流上增量维护前 N 名视图。
//
// 一个 Tracker 的 Apply 内部加锁，可被多个 goroutine 安全调用；
// 每条被接受的变更都会产出“先离开、后进入”的有序变化，被拒绝的变更不改变
// 任何状态，也不产生变化。快照与查询可被并发读取，返回的均为独立副本，
// 因而逐字段一致、不会被后续变更撕裂。
type TopNTracker struct {
	mu sync.Mutex

	n     int
	limit int

	// live 为全部存活行（含榜外）：键 -> 存活分数。
	live map[string]Score
	// ordered 为 live 的有序缓存，名次从 1 开始。
	ordered []Entry
	// log 为已产生的变化日志，每条被接受的变更对应一项。
	log []LoggedChange
	// seq 为已接受变更的单调序号。
	seq int
}

// LoggedChange 是变化日志中的一条记录：输入、判定依据与输出变化。
type LoggedChange struct {
	// Seq 为该变更在接受序列中的序号（从 1 开始）。
	Seq int
	// Input 为原始输入变更。
	Input Change
	// Accepted 恒为 true；日志中只记录被接受的变更。
	Accepted bool
	// Left 先于 Entered；下游必须先应用离开再应用进入。
	Left []Entry
	// Entered 为进入前 N 名的行。
	Entered []Entry
	// TopAfter 为应用后的前 N 名快照。
	TopAfter []Entry
}

// NewTopNTracker 创建一个维护前 n 名的 tracker。
// maxLiveRows 为存活行数上限，0 表示不限；n 必须为正数，maxLiveRows 不可为负。
// 参数非法时返回 RejectInvalidArgument 对应的错误，调用方可用 errors.Is 区分。
func NewTopNTracker(n int, maxLiveRows int) (*TopNTracker, error) {
	if n <= 0 || maxLiveRows < 0 {
		return nil, ErrInvalidArgument
	}
	return &TopNTracker{
		n:     n,
		limit: maxLiveRows,
		live:  make(map[string]Score),
	}, nil
}

// Apply 处理一条变更，返回判定结果与引起的榜单变化。
// 被拒绝时存活行、前 N 名与日志均不变；返回值中的切片为独立副本。
func (t *TopNTracker) Apply(c Change) ChangeResult {
	t.mu.Lock()
	defer t.mu.Unlock()

	if reason := validateChange(c); reason != RejectNone {
		return t.rejectResult(c, reason)
	}

	switch c.Kind {
	case KindAdd:
		if _, exists := t.live[c.Row.Key]; exists {
			return t.rejectResult(c, RejectDuplicateKey)
		}
		if t.limit > 0 && len(t.live) >= t.limit {
			return t.rejectResult(c, RejectTooManyLiveRows)
		}
	case KindRetract:
		liveScore, ok := t.live[c.Row.Key]
		if !ok {
			return t.rejectResult(c, RejectKeyNotFound)
		}
		if liveScore != c.Row.Score {
			return t.rejectResult(c, RejectScoreMismatch)
		}
	}

	// 通过全部校验：记录变更前快照，再执行状态变更。
	before := t.ordered

	switch c.Kind {
	case KindAdd:
		t.live[c.Row.Key] = c.Row.Score
	case KindRetract:
		delete(t.live, c.Row.Key)
	}

	t.ordered = computeRanks(t.live)
	left, entered := diffTopN(before, t.ordered, t.n)
	topAfter := topEntries(t.ordered, t.n)

	t.seq++
	t.log = append(t.log, LoggedChange{
		Seq:      t.seq,
		Input:    c,
		Accepted: true,
		Left:     cloneEntries(left),
		Entered:  cloneEntries(entered),
		TopAfter: cloneEntries(topAfter),
	})

	return ChangeResult{
		Input:    c,
		Accepted: true,
		Reason:   RejectNone,
		Left:     cloneEntries(left),
		Entered:  cloneEntries(entered),
		Top:      cloneEntries(topAfter),
	}
}

// rejectResult 构造拒绝结果；调用前不得修改任何状态。
func (t *TopNTracker) rejectResult(c Change, reason RejectReason) ChangeResult {
	return ChangeResult{
		Input:    c,
		Accepted: false,
		Reason:   reason,
		Left:     []Entry{},
		Entered:  []Entry{},
		Top:      []Entry{},
	}
}

// Snapshot 返回当前视图的一致性只读快照（字段均为副本）。
func (t *TopNTracker) Snapshot() View {
	t.mu.Lock()
	defer t.mu.Unlock()

	all := cloneEntries(t.ordered)
	return View{
		Top:       topEntries(t.ordered, t.n),
		All:       all,
		LiveCount: len(t.ordered),
	}
}

// TopN 返回当前前 N 名的副本（名次从 1 开始）。
func (t *TopNTracker) TopN() []Entry {
	t.mu.Lock()
	defer t.mu.Unlock()
	return topEntries(t.ordered, t.n)
}

// LiveCount 返回当前存活行数。
func (t *TopNTracker) LiveCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.ordered)
}

// ChangeLog 返回已产生的变化日志的副本。
func (t *TopNTracker) ChangeLog() []LoggedChange {
	t.mu.Lock()
	defer t.mu.Unlock()

	out := make([]LoggedChange, len(t.log))
	for i, lc := range t.log {
		out[i] = LoggedChange{
			Seq:      lc.Seq,
			Input:    lc.Input,
			Accepted: lc.Accepted,
			Left:     cloneEntries(lc.Left),
			Entered:  cloneEntries(lc.Entered),
			TopAfter: cloneEntries(lc.TopAfter),
		}
	}
	return out
}
