package replication

import (
	"errors"
	"log"
	"os"
	"sort"
	"sync"
)

// RecordType 是日志记录类型。
type RecordType int

const (
	// Begin 表示一个事务的起点。
	Begin RecordType = iota
	// Data 表示事务内的数据变更。
	Data
	// Commit 表示事务提交。
	Commit
	// Abort 表示事务中止。
	Abort
)

// Record 是一条携带递增序号（LSN）的日志记录，不同事务的记录可交错出现。
type Record struct {
	LSN     uint64
	Type    RecordType
	XID     uint64
	Payload string
}

// Transaction 是一个完整发出的事务：起点 BeginLSN、提交位点 CommitLSN
// 以及从 Begin 起收集到的全部记录（含 Begin，不含 Commit 本身）。
type Transaction struct {
	XID       uint64
	BeginLSN  uint64
	CommitLSN uint64
	Records   []Record
}

var (
	// ErrIllegalRecord 日志记录非法（LSN 未递增、类型未知或与事务状态不符）。
	ErrIllegalRecord = errors.New("illegal record")
	// ErrInvalidConfirmBoundary 确认位点没有恰好落在某个已发出事务的提交位点上。
	ErrInvalidConfirmBoundary = errors.New("confirm lsn is not a committed transaction boundary")
	// ErrConfirmRollback 确认位点回退（小于当前确认位点）。
	ErrConfirmRollback = errors.New("confirm lsn must not move backwards")
	// ErrTooManyInProgress 进行中事务数量超过上限。
	ErrTooManyInProgress = errors.New("too many in-progress transactions")
)

// DefaultMaxInProgress 是进行中事务的默认上限。
const DefaultMaxInProgress = 64

// Slot 是逻辑复制槽。零值不可用，请使用 NewSlot 构造。
//
// 位点不变式：0 <= restartLSN <= confirmedLSN <= 已见最大 LSN，
// 且两个位点在任何操作下都单调不减。
type Slot struct {
	mu           sync.Mutex
	maxInProg    int
	logger       *log.Logger
	logs         []Record                // 保留的日志记录，LSN 严格递增
	lastAppended uint64                  // 已接受的最大 LSN（0 表示尚无记录）
	confirmed    uint64                  // 确认位点 confirmed_flush_lsn
	restart      uint64                  // 重启位点 restart_lsn
	active       map[uint64]*Transaction // XID -> 进行中事务
	committed    []*Transaction          // 已提交未确认的事务，按提交 LSN 升序
	byCommit     map[uint64]*Transaction // 提交 LSN -> 已发出事务（用于校验确认边界）
	emitted      []*Transaction          // 待消费的已发出事务队列
}

// Option 配置 Slot。
type Option func(*Slot)

// WithMaxInProgress 设置进行中事务数量上限（必须大于 0）。
func WithMaxInProgress(n int) Option {
	return func(s *Slot) {
		if n > 0 {
			s.maxInProg = n
		}
	}
}

// WithLogger 设置判定日志输出位置。
func WithLogger(l *log.Logger) Option { return func(s *Slot) { s.logger = l } }

// NewSlot 创建一个空的复制槽。
func NewSlot(opts ...Option) *Slot {
	s := &Slot{
		maxInProg: DefaultMaxInProgress,
		logger:    log.New(os.Stderr, "[replication] ", log.LstdFlags|log.Lmicroseconds),
		active:    map[uint64]*Transaction{},
		byCommit:  map[uint64]*Transaction{},
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// validateRecord 必须在持锁状态下调用，只做判定，不修改状态。
func (s *Slot) validateRecord(rec Record) error {
	if rec.Type < Begin || rec.Type > Abort {
		return errors.Join(ErrIllegalRecord, errors.New("unknown record type"))
	}
	if rec.LSN == 0 || rec.LSN <= s.lastAppended {
		return errors.Join(ErrIllegalRecord, errors.New("lsn must be strictly increasing and positive"))
	}
	if rec.XID == 0 {
		return errors.Join(ErrIllegalRecord, errors.New("xid must be positive"))
	}
	_, open := s.active[rec.XID]
	switch rec.Type {
	case Begin:
		if open {
			return errors.Join(ErrIllegalRecord, errors.New("transaction already in progress"))
		}
		if len(s.active) >= s.maxInProg {
			return ErrTooManyInProgress
		}
	case Data, Commit, Abort:
		if !open {
			return errors.Join(ErrIllegalRecord, errors.New("no in-progress transaction for xid"))
		}
	}
	return nil
}

// Append 追加一条日志记录并驱动解码：Begin 开启事务，Data 累积，
// Commit 把整个事务作为一个原子单位发出，Abort 则丢弃该事务。
// 被拒绝时返回对应的哨兵错误，且日志、回收位置、解码状态与两个位点均不变。
func (s *Slot) Append(rec Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.validateRecord(rec); err != nil {
		s.logger.Printf("append REJECT lsn=%d xid=%d type=%s: %v", rec.LSN, rec.XID, typeName(rec.Type), err)
		return err
	}

	s.logs = append(s.logs, rec)
	s.lastAppended = rec.LSN

	var txn *Transaction
	switch rec.Type {
	case Begin:
		txn = &Transaction{XID: rec.XID, BeginLSN: rec.LSN, Records: []Record{rec}}
		s.active[rec.XID] = txn
	case Data:
		txn = s.active[rec.XID]
		txn.Records = append(txn.Records, rec)
	case Commit:
		txn = s.active[rec.XID]
		txn.CommitLSN = rec.LSN
		delete(s.active, rec.XID)
		s.committed = append(s.committed, txn)
		s.byCommit[rec.LSN] = txn
		s.emitted = append(s.emitted, txn)
		s.logger.Printf("append COMMIT emit xid=%d begin=%d commit=%d records=%d",
			txn.XID, txn.BeginLSN, txn.CommitLSN, len(txn.Records))
	case Abort:
		txn = s.active[rec.XID]
		delete(s.active, rec.XID)
		s.logger.Printf("append ABORT discard xid=%d begin=%d abort=%d records=%d",
			txn.XID, txn.BeginLSN, rec.LSN, len(txn.Records))
	}

	s.recomputeRestart()
	s.logger.Printf("append ACCEPT lsn=%d xid=%d type=%s confirmed=%d restart=%d kept=%d active=%d committed-pending=%d",
		rec.LSN, rec.XID, typeName(rec.Type), s.confirmed, s.restart, len(s.logs), len(s.active), len(s.committed))
	return nil
}

// Confirm 推进确认位点；flushLSN 必须恰好等于某个已发出事务的提交位点，
// 且不得小于当前确认位点。被拒绝时任何状态均不变。
func (s *Slot) Confirm(flushLSN uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var err error
	switch {
	case flushLSN == 0:
		err = errors.Join(ErrInvalidConfirmBoundary,
			errors.New("lsn 0 is never a committed transaction boundary"))
	case flushLSN < s.confirmed:
		err = errors.Join(ErrConfirmRollback,
			errors.New("requested below current confirmed lsn"))
	case flushLSN == s.confirmed:
		// 幂等重复确认：位点不变，成功返回。
	default:
		if s.byCommit[flushLSN] == nil {
			err = errors.Join(ErrInvalidConfirmBoundary,
				errors.New("no emitted transaction committed at lsn"))
		}
	}
	if err != nil {
		s.logger.Printf("confirm REJECT flush=%d confirmed=%d restart=%d: %v",
			flushLSN, s.confirmed, s.restart, err)
		return err
	}

	prev := s.confirmed
	s.confirmed = flushLSN
	kept := s.committed
	s.committed = s.committed[:0]
	for _, txn := range kept {
		if txn.CommitLSN <= flushLSN {
			delete(s.byCommit, txn.CommitLSN)
		} else {
			s.committed = append(s.committed, txn)
		}
	}
	s.recomputeRestart()
	s.logger.Printf("confirm ACCEPT flush=%d (was %d) restart=%d kept=%d active=%d committed-pending=%d emitted-pending=%d",
		flushLSN, prev, s.restart, len(s.logs), len(s.active), len(s.committed), len(s.emitted))
	return nil
}

// Emitted 取出并清空自上次调用以来新发出的事务（按提交位点升序）。
// 发出队列为空时返回 nil。
func (s *Slot) Emitted() []*Transaction {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := s.emitted
	sort.Slice(out, func(i, j int) bool { return out[i].CommitLSN < out[j].CommitLSN })
	s.emitted = nil
	s.logger.Printf("emitted drain count=%d confirmed=%d restart=%d", len(out), s.confirmed, s.restart)
	return out
}

// Restart 模拟崩溃重启：丢弃所有易失的解码状态，仅依据保留的日志（lsn >= restart）
// 重放重建。进行中事务恢复为进行中；已提交但未确认的事务会被重新发出，
// 因此崩溃重启后“不重不漏”。两个位点保持崩溃前的值（满足单调）。
func (s *Slot) Restart() {
	s.mu.Lock()
	defer s.mu.Unlock()

	keptLogs := s.logs
	confirmed := s.confirmed
	restart := s.restart

	active := map[uint64]*Transaction{}
	committed := make([]*Transaction, 0)
	byCommit := map[uint64]*Transaction{}
	var emitted []*Transaction

	for _, rec := range keptLogs {
		switch rec.Type {
		case Begin:
			active[rec.XID] = &Transaction{XID: rec.XID, BeginLSN: rec.LSN, Records: []Record{rec}}
		case Data:
			txn := active[rec.XID]
			txn.Records = append(txn.Records, rec)
		case Commit:
			txn := active[rec.XID]
			txn.CommitLSN = rec.LSN
			delete(active, rec.XID)
			if rec.LSN > confirmed {
				committed = append(committed, txn)
				byCommit[rec.LSN] = txn
				emitted = append(emitted, txn)
			}
		case Abort:
			delete(active, rec.XID)
		}
	}
	sort.Slice(emitted, func(i, j int) bool { return emitted[i].CommitLSN < emitted[j].CommitLSN })

	s.active = active
	s.committed = committed
	s.byCommit = byCommit
	s.emitted = emitted

	s.logger.Printf("restart replay kept=%d confirmed=%d restart=%d active=%d reemit=%d",
		len(keptLogs), confirmed, restart, len(active), len(emitted))
}

// ConfirmedLSN 返回当前确认位点。
func (s *Slot) ConfirmedLSN() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.confirmed
}

// RestartLSN 返回当前重启位点。
func (s *Slot) RestartLSN() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.restart
}

// recomputeRestart 在持锁状态下重算重启位点并回收日志。
//
// 重启位点取确认位点，与所有“进行中”或“已提交但未确认”事务起点
// （BeginLSN）的较小值；早于重启位点的日志被回收。
// 由于确认位点单调、保留集合只会缩小或出现更大的新起点，重启位点单调不减，
// 且恒有 restartLSN <= confirmedLSN。
func (s *Slot) recomputeRestart() {
	next := s.confirmed
	for _, txn := range s.active {
		if txn.BeginLSN < next {
			next = txn.BeginLSN
		}
	}
	for _, txn := range s.committed {
		if txn.BeginLSN < next {
			next = txn.BeginLSN
		}
	}

	if next < s.restart {
		next = s.restart
	}
	s.restart = next

	drop := 0
	for drop < len(s.logs) && s.logs[drop].LSN < s.restart {
		drop++
	}
	if drop > 0 {
		s.logs = append([]Record(nil), s.logs[drop:]...)
	}
}

func typeName(t RecordType) string {
	switch t {
	case Begin:
		return "BEGIN"
	case Data:
		return "DATA"
	case Commit:
		return "COMMIT"
	case Abort:
		return "ABORT"
	default:
		return "UNKNOWN"
	}
}
