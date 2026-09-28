package replication

import (
	"strconv"
	"sync"
)

// Store 是带前像校验的副本应用组件，并发安全。
//
// 读操作通过 RLock 并发进行；Apply 持写锁在临时副本上完成整批干跑与验证，
// 全部通过后一次性替换内部状态，因此读者只能看到某一批之前或之后的完整
// 边界状态，不会看到半批。被拒绝的批不会留下任何痕迹。
type Store struct {
	mu       sync.RWMutex
	rows     map[string]Row
	lastSeq  int64
	maxRows  int
	conflict []Conflict
	logger   DecisionLogger
}

// NewStore 创建副本组件。maxRows <= 0 表示不限制副本行数；
// logger 为 nil 时丢弃判定日志。
func NewStore(maxRows int, logger DecisionLogger) *Store {
	if logger == nil {
		logger = NewNopLogger()
	}
	return &Store{
		rows:    make(map[string]Row),
		maxRows: maxRows,
		logger:  logger,
	}
}

// Apply 尝试整批应用事件。
//
// 处理顺序（全部在写锁内、临时副本上完成）：
//  1. 逐条做结构合法性校验，非法 => ILLEGAL_EVENT；
//  2. 序号必须从 LastSeq()+1 起逐条 +1 连续，否则 => SEQUENCE_GAP；
//  3. 在前像校验下模拟执行，INSERT 新增行会超行数上限 => REPLICA_FULL；
//  4. 以上全部通过才一次性提交：替换副本、推进序号、追加冲突日志并输出判定日志。
//
// 冲突（行不存在 / 前像不符）不是错误：对应事件被跳过并记入
// BatchResult.Conflicts，同批其余事件照常应用。
func (s *Store) Apply(events []Event) (BatchResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	working := make(map[string]Row, len(s.rows)+len(events))
	for k, r := range s.rows {
		working[k] = cloneRow(r)
	}

	result := BatchResult{LastSeq: s.lastSeq}
	decisions := make([]Decision, 0, len(events))

	// addConflict 记录冲突并生成对应判定。
	addConflict := func(e Event, kind ConflictKind, basis string, current Row) {
		cf := Conflict{
			Seq: e.Seq, Key: e.Key, Op: e.Op, Kind: kind,
			Before:  cloneRow(e.Before),
			Current: cloneRow(current),
			Detail:  basis,
		}
		result.Conflicts = append(result.Conflicts, cf)
		decisions = append(decisions, Decision{
			Seq: e.Seq, Op: e.Op, Key: e.Key,
			Before: cloneRow(e.Before), After: cloneRow(e.After),
			Verdict: VerdictConflict, Basis: basis, Conflict: &cf,
		})
	}
	// applied 记录一条成功判定。
	applied := func(e Event, basis string) {
		result.Applied = append(result.Applied, e.Seq)
		decisions = append(decisions, Decision{
			Seq: e.Seq, Op: e.Op, Key: e.Key,
			Before: cloneRow(e.Before), After: cloneRow(e.After),
			Verdict: VerdictApplied, Basis: basis,
		})
	}

	for i, e := range events {
		expectedSeq := s.lastSeq + 1 + int64(i)

		if reason := validateEvent(e); reason != "" {
			s.logger.LogRejection(RejectIllegalEvent, reason)
			return BatchResult{}, &RejectError{
				Reason: RejectIllegalEvent, EventIndex: i, Seq: e.Seq, Detail: reason,
			}
		}
		if e.Seq != expectedSeq {
			detail := "event seq " + strconv.FormatInt(e.Seq, 10) +
				" is not consecutive; expected " + strconv.FormatInt(expectedSeq, 10)
			s.logger.LogRejection(RejectSequenceGap, detail)
			return BatchResult{}, &RejectError{
				Reason: RejectSequenceGap, EventIndex: i, Seq: e.Seq,
				ExpectedSeq: expectedSeq, Detail: detail,
			}
		}

		current, exists := working[e.Key]
		switch e.Op {
		case OpInsert:
			if exists {
				addConflict(e, ConflictRowExists,
					"INSERT requires the row to be absent, but it already exists", current)
				continue
			}
			if s.maxRows > 0 && len(working)+1 > s.maxRows {
				detail := "inserting key " + strconv.Quote(e.Key) + " would raise row count to " +
					strconv.Itoa(len(working)+1) + ", exceeding max_rows=" + strconv.Itoa(s.maxRows)
				s.logger.LogRejection(RejectReplicaFull, detail)
				return BatchResult{}, &RejectError{
					Reason: RejectReplicaFull, EventIndex: i, Seq: e.Seq, Detail: detail,
				}
			}
			working[e.Key] = cloneRow(e.After)
			applied(e, "row absent as required by the nil before-image; after-image inserted")

		case OpUpdate:
			switch {
			case !exists:
				addConflict(e, ConflictRowMissing,
					"UPDATE requires an existing row matching the before-image, but the row does not exist", nil)
			case !rowsEqual(e.Before, current):
				addConflict(e, ConflictBeforeMismatch,
					"before-image does not equal the current row: "+describeDiff(firstDiff(e.Before, current)),
					current)
			default:
				working[e.Key] = cloneRow(e.After)
				applied(e, "before-image equals the current row; after-image applied")
			}

		case OpDelete:
			switch {
			case !exists:
				addConflict(e, ConflictRowMissing,
					"DELETE requires an existing row matching the before-image, but the row does not exist", nil)
			case !rowsEqual(e.Before, current):
				addConflict(e, ConflictBeforeMismatch,
					"before-image does not equal the current row: "+describeDiff(firstDiff(e.Before, current)),
					current)
			default:
				delete(working, e.Key)
				applied(e, "before-image equals the current row; row deleted")
			}
		}
	}

	// 全部通过：一次性提交。
	s.rows = working
	s.lastSeq += int64(len(events))
	s.conflict = append(s.conflict, result.Conflicts...)
	result.LastSeq = s.lastSeq
	result.RowCount = len(s.rows)
	for _, d := range decisions {
		s.logger.LogDecision(d)
	}
	return result, nil
}

// Get 返回主键对应行的拷贝；不存在时 ok 为 false。
func (s *Store) Get(key string) (row Row, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, exists := s.rows[key]
	if !exists {
		return nil, false
	}
	return cloneRow(r), true
}

// Snapshot 返回副本全量深拷贝。
func (s *Store) Snapshot() map[string]Row {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]Row, len(s.rows))
	for k, r := range s.rows {
		out[k] = cloneRow(r)
	}
	return out
}

// RowCount 返回当前副本行数。
func (s *Store) RowCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.rows)
}

// LastSeq 返回已处理的连续序号上界。
func (s *Store) LastSeq() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastSeq
}

// Conflicts 返回冲突日志的深拷贝。
func (s *Store) Conflicts() []Conflict {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Conflict, len(s.conflict))
	for i, c := range s.conflict {
		c.Before = cloneRow(c.Before)
		c.Current = cloneRow(c.Current)
		out[i] = c
	}
	return out
}

// cloneRow 返回行镜像的独立拷贝；nil 保持 nil（区分"行不存在"）。
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
