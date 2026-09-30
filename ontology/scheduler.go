package ontology

import (
	"io"
	"log/slog"
	"sort"
	"sync"
)

// AbortReason 描述事务被中止的原因。
type AbortReason string

const (
	// AbortReadLate 表示「读过晚」：事务时间戳小于键的写时间戳。
	AbortReadLate AbortReason = "读过晚"
	// AbortWriteLate 表示「写过晚」：事务时间戳小于键的读时间戳。
	AbortWriteLate AbortReason = "写过晚"
)

// TxStatus 是事务的生命周期状态。
type TxStatus string

const (
	TxActive    TxStatus = "active"
	TxCommitted TxStatus = "committed"
	TxAborted   TxStatus = "aborted"
)

// RejectCode 标识操作被整体拒绝（不改变任何状态）的原因。
type RejectCode string

const (
	RejectEmptyKey    RejectCode = "empty_key"
	RejectNoSuchTx    RejectCode = "no_such_transaction"
	RejectTxCommitted RejectCode = "transaction_committed"
	RejectTxAborted   RejectCode = "transaction_aborted"
)

// RejectError 表示操作被拒绝；拒绝不改变任何调度器状态。
type RejectError struct {
	Code RejectCode
}

func (e *RejectError) Error() string { return string(e.Code) }

// AbortError 表示操作导致活动事务被中止（这是合法的事务结果）。
type AbortError struct {
	Reason AbortReason
}

func (e *AbortError) Error() string { return string(e.Reason) }

// KeyState 是单个键对外可见的时间戳与值记录。
type KeyState struct {
	ReadTS  int64
	WriteTS int64
	Value   int64
}

// TxInfo 是单个事务对外可见的快照。
type TxInfo struct {
	TS     int64
	Status TxStatus
	Buffer map[string]int64
}

// CommitResult 汇总一次提交的安装情况。
type CommitResult struct {
	TS        int64
	Installed []string
	Ignored   []string
}

// Scheduler 是基于时间戳排序（Basic Timestamp Ordering）的事务调度器。
type Scheduler struct {
	mu     sync.Mutex
	nextTS int64
	txs    map[int64]*txState
	keys   map[string]*keyRecord
	logger *slog.Logger
}

type txState struct {
	ts     int64
	status TxStatus
	buffer map[string]int64
}

type keyRecord struct {
	readTS  int64
	writeTS int64
	value   int64
}

// SetLogger 替换调度器使用的结构化日志器。
func (s *Scheduler) SetLogger(logger *slog.Logger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if logger != nil {
		s.logger = logger
	}
}

// Begin 开启一个新事务并返回其时间戳。
func (s *Scheduler) Begin() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextTS++
	ts := s.nextTS
	s.txs[ts] = &txState{ts: ts, status: TxActive, buffer: map[string]int64{}}
	s.logger.Info("begin", "input", "BEGIN", "output_ts", ts, "basis", "分配从1起连续递增的时间戳")
	return ts
}

// Read 执行一次读，返回读到的值；中止返回 *AbortError，拒绝返回 *RejectError。
func (s *Scheduler) Read(ts int64, key string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logger.Info("read-enter", "input", map[string]any{"op": "READ", "ts": ts, "key": key})
	if err := s.checkOp(ts, key); err != nil {
		s.logger.Info("read-rejected", "output", err.Error(), "basis", "拒绝：键为空/事务不存在或已终结，状态不变")
		return 0, err
	}
	tx := s.txs[ts]
	if v, ok := tx.buffer[key]; ok {
		s.logger.Info("read-own-write", "output", v, "basis", "缓冲已有该键的写，返回缓冲值且不触碰键的任何记录")
		return v, nil
	}
	rec := s.recordLocked(key)
	if ts < rec.writeTS {
		s.abortLocked(tx)
		err := &AbortError{Reason: AbortReadLate}
		s.logger.Info("read-aborted", "output", err.Error(),
			"basis", slog.GroupValue(slog.Int64("ts", ts), slog.Int64("writeTS", rec.writeTS)),
			"rule", "ts < 写时间戳 => 读过晚，中止并丢弃缓冲")
		return 0, err
	}
	v := rec.value
	if ts > rec.readTS {
		rec.readTS = ts
	}
	s.logger.Info("read-ok", "output", v, "basis",
		slog.Group("timestamps", slog.Int64("ts", ts), slog.Int64("readTS", rec.readTS), slog.Int64("writeTS", rec.writeTS)),
		"rule", "ts >= 写时间戳，返回当前值，读时间戳抬到 max(原读时间戳, ts)")
	return v, nil
}

// Write 把写记入事务私有缓冲；中止返回 *AbortError，拒绝返回 *RejectError。
func (s *Scheduler) Write(ts int64, key string, value int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logger.Info("write-enter", "input", map[string]any{"op": "WRITE", "ts": ts, "key": key, "value": value})
	if err := s.checkOp(ts, key); err != nil {
		s.logger.Info("write-rejected", "output", err.Error(), "basis", "拒绝：键为空/事务不存在或已终结，状态不变")
		return err
	}
	tx := s.txs[ts]
	// 写只与读时间戳比较；键从未被读过时读时间戳按初值 0 处理，不提前建记录。
	var readTS int64
	if rec, ok := s.keys[key]; ok {
		readTS = rec.readTS
	}
	if ts < readTS {
		s.abortLocked(tx)
		err := &AbortError{Reason: AbortWriteLate}
		s.logger.Info("write-aborted", "output", err.Error(),
			"basis", slog.GroupValue(slog.Int64("ts", ts), slog.Int64("readTS", readTS)),
			"rule", "ts < 读时间戳 => 写过晚，立即中止且缓冲不落盘")
		return err
	}
	tx.buffer[key] = value
	s.logger.Info("write-buffered", "output", "buffered",
		"basis", slog.GroupValue(slog.Int64("ts", ts), slog.Int64("readTS", readTS)),
		"rule", "ts >= 读时间戳，只写入事务私有缓冲，不改变键的任何记录")
	return nil
}

// Commit 提交事务：先按键升序复查，再逐个安装或忽略缓冲写。
func (s *Scheduler) Commit(ts int64) (*CommitResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logger.Info("commit-enter", "input", map[string]any{"op": "COMMIT", "ts": ts})
	if err := s.checkCommit(ts); err != nil {
		s.logger.Info("commit-rejected", "output", err.Error(), "basis", "拒绝：事务不存在或已终结，状态不变")
		return nil, err
	}
	tx := s.txs[ts]
	keys := make([]string, 0, len(tx.buffer))
	for k := range tx.buffer {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		rec := s.recordLocked(k)
		if ts < rec.readTS {
			s.abortLocked(tx)
			err := &AbortError{Reason: AbortWriteLate}
			s.logger.Info("commit-recheck-aborted", "output", err.Error(),
				"basis", slog.GroupValue(slog.String("key", k), slog.Int64("ts", ts), slog.Int64("readTS", rec.readTS)),
				"rule", "提交复查按键升序；任一键 ts < 读时间戳 => 整体中止，零安装")
			return nil, err
		}
	}

	res := &CommitResult{TS: ts, Installed: []string{}, Ignored: []string{}}
	for _, k := range keys {
		rec := s.recordLocked(k)
		v := tx.buffer[k]
		if ts < rec.writeTS {
			res.Ignored = append(res.Ignored, k)
			s.logger.Info("commit-write-ignored",
				"basis", slog.GroupValue(slog.String("key", k), slog.Int64("ts", ts), slog.Int64("writeTS", rec.writeTS)),
				"rule", "ts < 写时间戳 => 过时写被忽略，值与写时间戳都不变")
			continue
		}
		rec.value = v
		rec.writeTS = ts
		res.Installed = append(res.Installed, k)
		s.logger.Info("commit-write-installed",
			"basis", slog.GroupValue(slog.String("key", k), slog.Int64("ts", ts), slog.Int64("writeTS", rec.writeTS), slog.Int64("value", v)),
			"rule", "ts >= 写时间戳 => 安装值并把写时间戳置为 ts")
	}
	tx.status = TxCommitted
	tx.buffer = map[string]int64{}
	s.logger.Info("commit-ok", "output", res, "basis", "复查全部通过，已按键升序完成安装/忽略")
	return res, nil
}

// checkOp 按「键为空 → 事务不存在 → 已提交 → 已中止」的顺序只报第一个拒绝原因。
func (s *Scheduler) checkOp(ts int64, key string) error {
	if key == "" {
		return &RejectError{Code: RejectEmptyKey}
	}
	tx, ok := s.txs[ts]
	if !ok {
		return &RejectError{Code: RejectNoSuchTx}
	}
	switch tx.status {
	case TxCommitted:
		return &RejectError{Code: RejectTxCommitted}
	case TxAborted:
		return &RejectError{Code: RejectTxAborted}
	}
	return nil
}

// checkCommit 用于提交：提交不带键，因此只检查事务本身，顺序保持不存在 → 已提交 → 已中止。
func (s *Scheduler) checkCommit(ts int64) error {
	tx, ok := s.txs[ts]
	if !ok {
		return &RejectError{Code: RejectNoSuchTx}
	}
	switch tx.status {
	case TxCommitted:
		return &RejectError{Code: RejectTxCommitted}
	case TxAborted:
		return &RejectError{Code: RejectTxAborted}
	}
	return nil
}

// recordLocked 返回键的记录；键第一次被读/写/复查到时按初值（0,0,0）创建。
func (s *Scheduler) recordLocked(key string) *keyRecord {
	rec, ok := s.keys[key]
	if !ok {
		rec = &keyRecord{}
		s.keys[key] = rec
	}
	return rec
}

func (s *Scheduler) abortLocked(tx *txState) {
	tx.status = TxAborted
	tx.buffer = map[string]int64{}
}

// TxInfo 返回事务快照；不存在时 ok 为 false。
func (s *Scheduler) TxInfo(ts int64) (TxInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, ok := s.txs[ts]
	if !ok {
		return TxInfo{}, false
	}
	buf := make(map[string]int64, len(tx.buffer))
	for k, v := range tx.buffer {
		buf[k] = v
	}
	return TxInfo{TS: tx.ts, Status: tx.status, Buffer: buf}, true
}

// KeyState 返回键当前的读/写时间戳与值；键从未出现时 ok 为 false。
func (s *Scheduler) KeyState(key string) (KeyState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.keys[key]
	if !ok {
		return KeyState{}, false
	}
	return KeyState{ReadTS: rec.readTS, WriteTS: rec.writeTS, Value: rec.value}, true
}

// Values 返回所有键当前值的快照副本。
func (s *Scheduler) Values() map[string]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int64, len(s.keys))
	for k, rec := range s.keys {
		out[k] = rec.value
	}
	return out
}

// NewScheduler 创建一个时间戳从 1 开始连续递增的调度器。
func NewScheduler() *Scheduler {
	return &Scheduler{
		nextTS: 0,
		txs:    map[int64]*txState{},
		keys:   map[string]*keyRecord{},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}
