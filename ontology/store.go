package ontology

import (
	"strconv"
	"sync"
)

// Store 是带前像校验的副本表。
//
// 并发模型：Apply 串行化（内部持写锁），Snapshot/LastSeq/Conflicts
// 持读锁并在锁内拷贝，因此并发读取看到的永远是某一批应用前或应用后
// 的完整边界状态，不会看到半批。被拒绝的批不改变副本、冲突日志与
// 已处理序号。
type Store struct {
	mu        sync.RWMutex
	rows      map[string]Row
	maxRows   int
	lastSeq   int64
	conflicts []Conflict
	logger    JudgeLogger
}

// judge 是影子应用阶段对单条事件的判定，提交成功后统一输出日志。
type judge struct {
	event  Event
	result string
	basis  string
}

// NewStore 创建一个空副本表，maxRows 为副本行数上限（必须为正）。
func NewStore(maxRows int) *Store {
	if maxRows <= 0 {
		maxRows = 1
	}
	return &Store{
		rows:    make(map[string]Row),
		maxRows: maxRows,
	}
}

// SetLogger 注入逐条事件的判定日志器，返回 s 便于链式调用。
func (s *Store) SetLogger(l JudgeLogger) *Store {
	s.mu.Lock()
	s.logger = l
	s.mu.Unlock()
	return s
}

// Apply 把一批事件按序逐条应用到副本表。
//
// 合法但前像不符的事件记为冲突并跳过，不改变副本；非法事件、序号不连续、
// 行数超限会拒绝整批，且不产生任何已落地的变更（副本、冲突日志、
// 已处理序号均保持批前状态）。
func (s *Store) Apply(events []Event) (*BatchResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 阶段一：纯校验。非法事件形状或序号不连续，立即拒绝，不触碰状态。
	for i, e := range events {
		if err := validateEvent(e); err != nil {
			err.Index = i
			s.logReject(e, err.Reason, err.Detail)
			return nil, err
		}
		wantSeq := s.lastSeq + int64(i) + 1
		if e.Seq != wantSeq {
			err := &RejectError{
				Reason: RejectSeqGap,
				Index:  i,
				Detail: "事件序号不连续：期望 " + strconv.FormatInt(wantSeq, 10) +
					"，实际 " + strconv.FormatInt(e.Seq, 10),
			}
			s.logReject(e, err.Reason, err.Detail)
			return nil, err
		}
	}

	// 阶段二：在影子副本上逐条判定。冲突只记录不落地变更；
	// 插入超限行数在实际插入点判定为整批拒绝。
	shadow := make(map[string]Row, len(s.rows))
	for k, r := range s.rows {
		shadow[k] = cloneRow(r)
	}
	judges := make([]judge, 0, len(events))
	batchConflicts := make([]Conflict, 0)
	applied := 0

	for i, e := range events {
		switch e.Op {
		case OpInsert:
			if _, exists := shadow[e.Key]; exists {
				c := Conflict{Seq: e.Seq, Key: e.Key, Op: e.Op,
					Kind:   ConflictRowExists,
					Reason: "插入目标行已存在，前像要求为不存在"}
				batchConflicts = append(batchConflicts, c)
				judges = append(judges, judge{e, "conflict:" + string(c.Kind), c.Reason})
				continue
			}
			if len(shadow) >= s.maxRows {
				err := &RejectError{
					Reason: RejectTooManyRows,
					Index:  i,
					Detail: "插入 seq=" + strconv.FormatInt(e.Seq, 10) +
						" 将使副本行数 " + strconv.Itoa(len(shadow)+1) +
						" 超过上限 " + strconv.Itoa(s.maxRows),
				}
				s.logReject(e, err.Reason, err.Detail)
				return nil, err
			}
			shadow[e.Key] = cloneRow(e.After)
			applied++
			judges = append(judges, judge{e, "applied",
				"目标行不存在且行数未超限，按后像插入"})

		case OpUpdate:
			current, exists := shadow[e.Key]
			if !exists {
				c := Conflict{Seq: e.Seq, Key: e.Key, Op: e.Op,
					Kind:   ConflictRowMissing,
					Reason: "更新目标行在副本中不存在"}
				batchConflicts = append(batchConflicts, c)
				judges = append(judges, judge{e, "conflict:" + string(c.Kind), c.Reason})
				continue
			}
			if !rowsEqual(current, e.Before) {
				c := Conflict{Seq: e.Seq, Key: e.Key, Op: e.Op,
					Kind: ConflictBeforeMismatch,
					Reason: "前像 " + formatRow(e.Before) +
						" 与当前行 " + formatRow(current) + " 不完全一致"}
				batchConflicts = append(batchConflicts, c)
				judges = append(judges, judge{e, "conflict:" + string(c.Kind), c.Reason})
				continue
			}
			shadow[e.Key] = cloneRow(e.After)
			applied++
			judges = append(judges, judge{e, "applied",
				"前像 " + formatRow(e.Before) + " 与当前行完全一致，按后像覆盖"})

		case OpDelete:
			current, exists := shadow[e.Key]
			if !exists {
				c := Conflict{Seq: e.Seq, Key: e.Key, Op: e.Op,
					Kind:   ConflictRowMissing,
					Reason: "删除目标行在副本中不存在"}
				batchConflicts = append(batchConflicts, c)
				judges = append(judges, judge{e, "conflict:" + string(c.Kind), c.Reason})
				continue
			}
			if !rowsEqual(current, e.Before) {
				c := Conflict{Seq: e.Seq, Key: e.Key, Op: e.Op,
					Kind: ConflictBeforeMismatch,
					Reason: "前像 " + formatRow(e.Before) +
						" 与当前行 " + formatRow(current) + " 不完全一致"}
				batchConflicts = append(batchConflicts, c)
				judges = append(judges, judge{e, "conflict:" + string(c.Kind), c.Reason})
				continue
			}
			delete(shadow, e.Key)
			applied++
			judges = append(judges, judge{e, "applied",
				"前像 " + formatRow(e.Before) + " 与当前行完全一致，删除该行"})
		}
	}

	// 阶段三：全部事件均未触发拒绝，原子提交。
	s.rows = shadow
	s.conflicts = append(s.conflicts, batchConflicts...)
	s.lastSeq += int64(len(events))
	for _, j := range judges {
		s.logJudge(j)
	}
	return &BatchResult{
		Applied:   applied,
		Conflicts: batchConflicts,
		LastSeq:   s.lastSeq,
	}, nil
}

// Snapshot 返回副本当前完整边界上的只读快照（深拷贝，无需持锁即可并发读）。
func (s *Store) Snapshot() *Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows := make(map[string]Row, len(s.rows))
	for k, r := range s.rows {
		rows[k] = cloneRow(r)
	}
	return &Snapshot{rows: rows}
}

// LastSeq 返回已处理的最后一个事件序号；尚无事件时为 0。
func (s *Store) LastSeq() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastSeq
}

// Conflicts 返回截至目前累计的冲突日志拷贝（按发生顺序）。
func (s *Store) Conflicts() []Conflict {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Conflict, len(s.conflicts))
	copy(out, s.conflicts)
	return out
}

// validateEvent 校验事件形状：操作类型已知且前后像组合符合该操作的要求。
func validateEvent(e Event) *RejectError {
	switch e.Op {
	case OpInsert:
		if e.Before != nil {
			return rejectInvalid(e, "insert 的前像必须为 nil（行不存在）")
		}
		if e.After == nil {
			return rejectInvalid(e, "insert 的后像不能为 nil")
		}
	case OpUpdate:
		if e.Before == nil {
			return rejectInvalid(e, "update 的前像不能为 nil")
		}
		if e.After == nil {
			return rejectInvalid(e, "update 的后像不能为 nil")
		}
	case OpDelete:
		if e.Before == nil {
			return rejectInvalid(e, "delete 的前像不能为 nil")
		}
		if e.After != nil {
			return rejectInvalid(e, "delete 的后像必须为 nil")
		}
	default:
		return rejectInvalid(e, "未知操作类型: "+string(e.Op))
	}
	return nil
}

func rejectInvalid(e Event, detail string) *RejectError {
	return &RejectError{Reason: RejectInvalidEvent, Index: -1, Detail: detail}
}

// logJudge 在提交成功后输出一条事件判定日志。
func (s *Store) logJudge(j judge) {
	if s.logger == nil {
		return
	}
	s.logger.Log(JudgeEntry{
		Seq: j.event.Seq, Key: j.event.Key, Op: j.event.Op,
		Input: j.event, Result: j.result, Basis: j.basis,
	})
}

// logReject 输出一条整批拒绝日志（日志仅为观测输出，不属于被回滚的状态）。
func (s *Store) logReject(e Event, reason RejectReason, detail string) {
	if s.logger == nil {
		return
	}
	s.logger.Log(JudgeEntry{
		Seq: e.Seq, Key: e.Key, Op: e.Op,
		Input: e, Result: "rejected:" + string(reason), Basis: detail,
	})
}

func cloneRow(r Row) Row {
	if r == nil {
		return nil
	}
	cp := make(Row, len(r))
	for k, v := range r {
		cp[k] = v
	}
	return cp
}
