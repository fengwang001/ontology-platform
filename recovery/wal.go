package recovery

import (
	"errors"
	"sort"
	"sync"
)

// Append 校验失败原因，按此顺序只报第一个。
var (
	// ErrLSNNotIncreasing LSN 不大于已有最大 LSN。
	ErrLSNNotIncreasing = errors.New("recovery: LSN 未严格递增")
	// ErrCheckpointBeginUnknown EndCkpt 的 begin 不是已有 BeginCkpt 的 LSN。
	ErrCheckpointBeginUnknown = errors.New("recovery: EndCkpt 的 begin 不是已有 BeginCkpt 的 LSN")
	// ErrCheckpointAlreadyEnded 该 BeginCkpt 已有对应 EndCkpt。
	ErrCheckpointAlreadyEnded = errors.New("recovery: 该 BeginCkpt 已有对应 EndCkpt")
	// ErrTxnAlreadyEnded 记录引用已 End 的事务。
	ErrTxnAlreadyEnded = errors.New("recovery: 记录引用已 End 的事务")
	// ErrTxnNeverSeen Commit/Abort/End 引用从未出现的事务。
	ErrTxnNeverSeen = errors.New("recovery: Commit/Abort/End 引用从未出现的事务")
)

// LogEntry 已追加的一条日志（LSN + 记录内容）。
type LogEntry struct {
	LSN    int64
	Record Record
}

// WAL 仅追加日志，支持 Append 与 Analyze 并发调用。
type WAL struct {
	mu      sync.RWMutex
	entries []LogEntry
	maxLSN  int64

	openBegins   map[int64]bool // 已出现但尚无 EndCkpt 的 BeginCkpt LSN
	closedBegins map[int64]bool // 已有 EndCkpt 的 BeginCkpt LSN
	seenTxns     map[int]bool   // 已出现的事务（含检查点快照中的事务）
	endedTxns    map[int]bool   // 已 End 的事务
}

// NewWAL 创建空日志。
func NewWAL() *WAL {
	return &WAL{
		openBegins:   make(map[int64]bool),
		closedBegins: make(map[int64]bool),
		seenTxns:     make(map[int]bool),
		endedTxns:    make(map[int]bool),
	}
}

// Append 校验并追加一条记录；校验失败时整体拒绝，日志不变。
func (w *WAL) Append(lsn int64, rec Record) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.validate(lsn, rec); err != nil {
		return err
	}
	w.apply(lsn, rec)
	return nil
}

func (w *WAL) validate(lsn int64, rec Record) error {
	// 1. LSN 必须严格递增。
	if lsn <= w.maxLSN {
		return ErrLSNNotIncreasing
	}
	// 2. EndCkpt 的 begin 必须是尚无 EndCkpt 的 BeginCkpt。
	if rec.Type == EndCkpt {
		if w.closedBegins[rec.Begin] {
			return ErrCheckpointAlreadyEnded
		}
		if !w.openBegins[rec.Begin] {
			return ErrCheckpointBeginUnknown
		}
	}
	// 3. 记录不得引用已 End 的事务。
	switch rec.Type {
	case Update, Commit, Abort, End:
		if w.endedTxns[rec.Txn] {
			return ErrTxnAlreadyEnded
		}
	}
	// 4. Commit/Abort/End 不得引用从未出现的事务。
	switch rec.Type {
	case Commit, Abort, End:
		if !w.seenTxns[rec.Txn] {
			return ErrTxnNeverSeen
		}
	}
	return nil
}

func (w *WAL) apply(lsn int64, rec Record) {
	w.entries = append(w.entries, LogEntry{LSN: lsn, Record: rec})
	w.maxLSN = lsn

	switch rec.Type {
	case Update, Commit, Abort:
		w.seenTxns[rec.Txn] = true
	case End:
		w.seenTxns[rec.Txn] = true
		w.endedTxns[rec.Txn] = true
	case BeginCkpt:
		w.openBegins[lsn] = true
	case EndCkpt:
		delete(w.openBegins, rec.Begin)
		w.closedBegins[rec.Begin] = true
		for txn := range rec.ATT {
			w.seenTxns[txn] = true
		}
	case PageFlush:
	}
}

// Entries 返回当前日志的快照副本。
func (w *WAL) Entries() []LogEntry {
	w.mu.RLock()
	defer w.mu.RUnlock()
	out := make([]LogEntry, len(w.entries))
	copy(out, w.entries)
	return out
}

// Analyze 执行 ARIES 分析阶段，不修改日志。
func (w *WAL) Analyze() Analysis {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return analyze(w.entries)
}

func analyze(entries []LogEntry) Analysis {
	dpt := make(map[int]int64)
	att := make(map[int]AttSnapshotEntry)

	// 取最后一个有对应 EndCkpt 的 BeginCkpt（begin LSN 最大者），
	// 用其 EndCkpt 的快照初始化两张表；没有完整检查点则从空表扫描全部日志。
	var startLSN int64
	foundCkpt := false
	for _, e := range entries {
		if e.Record.Type == EndCkpt && (!foundCkpt || e.Record.Begin > startLSN) {
			foundCkpt = true
			startLSN = e.Record.Begin
		}
	}
	if foundCkpt {
		for _, e := range entries {
			if e.Record.Type != EndCkpt || e.Record.Begin != startLSN {
				continue
			}
			for page, recLSN := range e.Record.DPT {
				dpt[page] = recLSN
			}
			for txn, entry := range e.Record.ATT {
				att[txn] = entry
			}
		}
	}

	// 按 LSN 顺序处理 LSN 大于 begin 的记录（检查点记录本身不处理）。
	for _, e := range entries {
		if foundCkpt && e.LSN <= startLSN {
			continue
		}
		rec := e.Record
		switch rec.Type {
		case Update:
			if entry, ok := att[rec.Txn]; ok {
				entry.LastLSN = e.LSN
				att[rec.Txn] = entry
			} else {
				att[rec.Txn] = AttSnapshotEntry{Status: Running, LastLSN: e.LSN}
			}
			if _, ok := dpt[rec.Page]; !ok {
				dpt[rec.Page] = e.LSN
			}
		case Commit:
			att[rec.Txn] = AttSnapshotEntry{Status: Committed, LastLSN: e.LSN}
		case Abort:
			att[rec.Txn] = AttSnapshotEntry{Status: Aborting, LastLSN: e.LSN}
		case End:
			delete(att, rec.Txn)
		case PageFlush:
			delete(dpt, rec.Page)
		case BeginCkpt, EndCkpt:
		}
	}

	return buildAnalysis(dpt, att)
}

func buildAnalysis(dpt map[int]int64, att map[int]AttSnapshotEntry) Analysis {
	result := Analysis{
		DPT:    make([]DirtyPage, 0, len(dpt)),
		ATT:    make([]AttEntry, 0, len(att)),
		Failed: []int{},
	}
	for page, recLSN := range dpt {
		result.DPT = append(result.DPT, DirtyPage{Page: page, RecLSN: recLSN})
	}
	sort.Slice(result.DPT, func(i, j int) bool { return result.DPT[i].Page < result.DPT[j].Page })

	for txn, entry := range att {
		result.ATT = append(result.ATT, AttEntry{Txn: txn, Status: entry.Status, LastLSN: entry.LastLSN})
		if entry.Status == Running || entry.Status == Aborting {
			result.Failed = append(result.Failed, txn)
		}
	}
	sort.Slice(result.ATT, func(i, j int) bool { return result.ATT[i].Txn < result.ATT[j].Txn })
	sort.Ints(result.Failed)

	if len(result.DPT) > 0 {
		result.NeedRedo = true
		result.RedoLSN = result.DPT[0].RecLSN
		for _, dp := range result.DPT[1:] {
			if dp.RecLSN < result.RedoLSN {
				result.RedoLSN = dp.RecLSN
			}
		}
	}
	return result
}
