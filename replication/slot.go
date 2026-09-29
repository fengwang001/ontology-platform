package replication

import (
	"fmt"
	"sync"
)

// Slot 是逻辑复制槽：维护确认位点与重启位点，并按需保留日志。
//
//   - 确认位点 confirmedLSN：消费端已确认的最大提交位点。
//   - 重启位点 restartLSN：确认位点与所有进行中 / 已提交未确认事务起点的最小值，
//     早于重启位点的日志被回收。
//
// 所有内部状态仅由持有 mu 的方法访问，Append、Confirm、Recover 可并发调用。
type Slot struct {
	mu            sync.Mutex
	maxInProgress int

	wal          []Record          // 保留的日志，LSN 严格递增
	lastLSN      LSN               // 已追加的最大 LSN
	inProgress   map[uint64]*txnSt // 进行中的事务（解码状态）
	pending      []Transaction     // 已提交但未确认的事务，按提交位点递增
	confirmedLSN LSN               // 确认位点
	restartLSN   LSN               // 重启位点
	retainedFrom LSN               // 回收位置：最早保留日志的序号
}

// txnSt 是进行中的事务在解码器内的缓存状态。
type txnSt struct {
	startLSN LSN
	records  []Record
}

// NewSlot 创建一个复制槽，maxInProgress 为允许的最大进行中事务数。
func NewSlot(maxInProgress int) *Slot {
	return &Slot{
		maxInProgress: maxInProgress,
		inProgress:    make(map[uint64]*txnSt),
	}
}

// Append 追加一条日志记录；若该记录使某事务提交，则返回发出的事务。
// 记录非法或进行中事务超限时返回错误，且不改变任何内部状态。
func (s *Slot) Append(rec Record) (*Transaction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.validate(rec); err != nil {
		return nil, err
	}

	s.wal = append(s.wal, rec)
	s.lastLSN = rec.LSN

	switch rec.Type {
	case RecordBegin:
		s.inProgress[rec.TxnID] = &txnSt{startLSN: rec.LSN}
	case RecordData:
		st := s.inProgress[rec.TxnID]
		st.records = append(st.records, rec)
	case RecordAbort:
		delete(s.inProgress, rec.TxnID)
		s.recomputeRestartLocked()
	case RecordCommit:
		st := s.inProgress[rec.TxnID]
		delete(s.inProgress, rec.TxnID)
		txn := Transaction{
			ID:        rec.TxnID,
			StartLSN:  st.startLSN,
			CommitLSN: rec.LSN,
			Records:   append([]Record(nil), st.records...),
		}
		s.pending = append(s.pending, txn)
		s.recomputeRestartLocked()
		return &txn, nil
	}
	return nil, nil
}

// validate 只做校验不修改状态，保证被拒绝的追加不产生任何副作用。
func (s *Slot) validate(rec Record) error {
	if rec.Type < RecordBegin || rec.Type > RecordAbort {
		return fmt.Errorf("%w: unknown record type %d at lsn %d", ErrInvalidRecord, rec.Type, rec.LSN)
	}
	if rec.LSN <= s.lastLSN {
		return fmt.Errorf("%w: lsn %d not greater than last lsn %d", ErrInvalidRecord, rec.LSN, s.lastLSN)
	}
	_, open := s.inProgress[rec.TxnID]
	switch rec.Type {
	case RecordBegin:
		if open {
			return fmt.Errorf("%w: txn %d already in progress", ErrInvalidRecord, rec.TxnID)
		}
		if len(s.inProgress) >= s.maxInProgress {
			return fmt.Errorf("%w: limit %d reached by txn %d", ErrTooManyInProgress, s.maxInProgress, rec.TxnID)
		}
	case RecordData, RecordCommit, RecordAbort:
		if !open {
			return fmt.Errorf("%w: txn %d not in progress", ErrInvalidRecord, rec.TxnID)
		}
	}
	return nil
}

// Confirm 将确认位点推进到 lsn，lsn 必须恰好是某个已发出事务的提交位点。
// 确认回退或未落在事务边界时返回错误，且不改变任何内部状态。
func (s *Slot) Confirm(lsn LSN) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if lsn <= s.confirmedLSN {
		return fmt.Errorf("%w: lsn %d not greater than confirmed lsn %d", ErrConfirmRegression, lsn, s.confirmedLSN)
	}
	idx := -1
	for i := range s.pending {
		if s.pending[i].CommitLSN == lsn {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("%w: lsn %d is not a commit lsn of any emitted transaction", ErrConfirmNotOnBoundary, lsn)
	}

	s.confirmedLSN = lsn
	s.pending = s.pending[idx+1:]
	s.recomputeRestartLocked()
	return nil
}

// Recover 模拟崩溃重启：清空解码状态，从重启位点重放保留日志，
// 重新发出所有已提交但未确认的事务（不重不漏，中止事务不重发）。
func (s *Slot) Recover() []Transaction {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.inProgress = make(map[uint64]*txnSt)
	s.pending = nil

	// 保留日志中可能缺少某些事务的 begin（已确认或已中止事务的
	// 前段日志已被回收），重放时这些记录没有解码上下文，直接跳过；
	// 已提交未确认事务的起点必然不小于重启位点，其 begin 一定保留。
	var emitted []Transaction
	for _, rec := range s.wal {
		switch rec.Type {
		case RecordBegin:
			s.inProgress[rec.TxnID] = &txnSt{startLSN: rec.LSN}
		case RecordData:
			if st, ok := s.inProgress[rec.TxnID]; ok {
				st.records = append(st.records, rec)
			}
		case RecordAbort:
			delete(s.inProgress, rec.TxnID)
		case RecordCommit:
			st, ok := s.inProgress[rec.TxnID]
			if !ok {
				continue
			}
			delete(s.inProgress, rec.TxnID)
			txn := Transaction{
				ID:        rec.TxnID,
				StartLSN:  st.startLSN,
				CommitLSN: rec.LSN,
				Records:   append([]Record(nil), st.records...),
			}
			s.pending = append(s.pending, txn)
			emitted = append(emitted, txn)
		}
	}
	s.recomputeRestartLocked()
	return emitted
}

// recomputeRestartLocked 重算重启位点并回收早于它的日志。
// 重启位点 = min(确认位点, 进行中与已提交未确认事务的最小起点)。
func (s *Slot) recomputeRestartLocked() {
	restart := s.confirmedLSN
	for _, st := range s.inProgress {
		if st.startLSN < restart {
			restart = st.startLSN
		}
	}
	for _, txn := range s.pending {
		if txn.StartLSN < restart {
			restart = txn.StartLSN
		}
	}
	s.restartLSN = restart

	cut := 0
	for cut < len(s.wal) && s.wal[cut].LSN < restart {
		cut++
	}
	if cut > 0 {
		s.wal = append([]Record(nil), s.wal[cut:]...)
	}
	if len(s.wal) > 0 {
		s.retainedFrom = s.wal[0].LSN
	} else {
		s.retainedFrom = restart
	}
}

// Positions 原子地返回（重启位点, 确认位点），保证重启位点不大于确认位点。
func (s *Slot) Positions() (restart, confirmed LSN) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.restartLSN, s.confirmedLSN
}

// RetainedFrom 返回当前保留的最早日志序号，早于它的日志已被回收。
func (s *Slot) RetainedFrom() LSN {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.retainedFrom
}
