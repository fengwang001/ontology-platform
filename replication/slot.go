package replication

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const walName = "wal.log"

// Option 配置复制槽。
type Option func(*Slot)

// WithMaxInProgress 设置进行中事务数上限，默认 DefaultMaxInProgress。
func WithMaxInProgress(n int) Option {
	return func(s *Slot) { s.maxInProgress = n }
}

// WithLogger 设置结构化日志（JSON 行）输出位置，默认为 os.Stderr。
func WithLogger(w io.Writer) Option {
	return func(s *Slot) { s.logWriter = w }
}

// Slot 是带持久化的逻辑复制槽：维护确认位点与重启位点，
// 按需保留/回收日志，崩溃重启后能重新发出已提交但未确认的事务。
//
// Append / Confirm 与位点读取可以并发调用；互斥锁保证：
//   - 被拒绝的操作不改变日志、回收位置、解码状态与两个位点；
//   - 并发读到的位点单调且始终满足 restart <= confirmed。
type Slot struct {
	mu            sync.Mutex
	dir           string
	wal           *os.File
	machine       *Machine
	maxInProgress int
	logWriter     io.Writer
	closed        bool
	// compactedLSN 是当前 WAL 文件中仍保留的最小 LSN；
	// machine.restartLSN >= compactedLSN，压缩后二者相等。
	compactedLSN uint64
}

// Open 打开（或新建）目录 dir 下的复制槽并完成崩溃恢复。
// 返回的槽就绪后，可用 Reemitted 取出重启后需重新发出的事务。
func Open(ctx context.Context, dir string, opts ...Option) (*Slot, error) {
	s := &Slot{
		dir:           dir,
		maxInProgress: DefaultMaxInProgress,
		logWriter:     os.Stderr,
	}
	for _, opt := range opts {
		opt(s)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	meta, _, err := readMetadata(dir)
	if err != nil {
		return nil, err
	}
	walPath := filepath.Join(dir, walName)
	f, err := os.OpenFile(walPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}
	retained, err := readRecords(f)
	if err != nil {
		f.Close()
		return nil, err
	}

	emitted, machine, err := Replay(retained, meta.ConfirmedLSN, s.maxInProgress)
	if err != nil {
		f.Close()
		return nil, err
	}
	s.machine = machine
	s.wal = f

	// 对齐压缩水位，必要时立刻重写一次 WAL。
	s.compactedLSN = 0
	if len(retained) > 0 {
		s.compactedLSN = retained[0].LSN
	}
	if err := s.compactLocked(ctx); err != nil {
		return nil, err
	}

	s.logLine("slot_open", map[string]any{
		"replayed_records": len(retained),
		"confirmed_lsn":    machine.ConfirmedLSN(),
		"restart_lsn":      machine.RestartLSN(),
		"reemitted":        emitted,
		"reason":           "recover retained WAL; commits above confirmed are re-emitted; aborted are dropped",
	})
	return s, nil
}

// Append 校验、持久化并解码一条日志记录。
// 事务提交时整体返回该事务；读中止返回 nil（事务被丢弃）。
func (s *Slot) Append(ctx context.Context, r Record) (txn *Transaction, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrSlotClosed
	}

	// 先在内存状态机上完整校验；被拒绝的操作不触碰任何状态或文件。
	if err = s.machine.validateRecord(r); err != nil {
		s.logReject("append", r, err)
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}

	// 校验通过后才落盘，fsync 成功后再推进内存状态，保证不丢已确认数据。
	if _, err = s.wal.Write(encodeRecord(r)); err != nil {
		return nil, err
	}
	if err = s.wal.Sync(); err != nil {
		return nil, err
	}

	beforeRestart := s.machine.RestartLSN()
	txn, err = s.machine.ApplyRecord(r)
	if err != nil {
		// 理论上不可达：校验已通过。
		return nil, err
	}

	result := map[string]any{
		"record":        r,
		"confirmed_lsn": s.machine.ConfirmedLSN(),
		"restart_lsn":   s.machine.RestartLSN(),
		"emitted":       txn,
		"reason":        s.appendReason(r, txn != nil, beforeRestart),
	}
	if err = s.compactLocked(ctx); err != nil {
		return nil, err
	}
	s.logLine("append", result)
	return txn, nil
}

// Confirm 推进确认位点。位点必须恰好等于某个已发出事务的提交位点，
// 且不能回退；任何拒绝都不改变状态。
func (s *Slot) Confirm(ctx context.Context, commitLSN uint64) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrSlotClosed
	}

	confirmed := s.machine.ConfirmedLSN()
	switch {
	case commitLSN < confirmed:
		err = ErrConfirmRewound
	case commitLSN == confirmed:
		err = nil
	case s.machine.findPending(commitLSN) == nil:
		err = ErrInvalidConfirm
	}
	if err != nil {
		s.logLine("confirm_rejected", map[string]any{
			"input_lsn":     commitLSN,
			"confirmed_lsn": confirmed,
			"restart_lsn":   s.machine.RestartLSN(),
			"reason":        err.Error(),
		})
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if commitLSN == confirmed {
		s.logLine("confirm", map[string]any{
			"input_lsn":     commitLSN,
			"confirmed_lsn": confirmed,
			"restart_lsn":   s.machine.RestartLSN(),
			"reason":        "idempotent confirm at current boundary; state unchanged",
		})
		return nil
	}

	before := s.machine.RestartLSN()
	// 先推进内存状态（已通过全部校验），随后按“元数据先于物理回收”的
	// 顺序落盘：即使崩溃发生在两步之间，恢复时也只是多保留日志，绝不丢失。
	if err = s.machine.Confirm(commitLSN); err != nil {
		return err
	}
	if err = writeMetadataAtomic(s.dir, slotMetadata{
		ConfirmedLSN: s.machine.ConfirmedLSN(),
		RestartLSN:   s.machine.RestartLSN(),
	}); err != nil {
		return err
	}
	if err = s.compactLocked(ctx); err != nil {
		return err
	}
	s.logLine("confirm", map[string]any{
		"input_lsn":     commitLSN,
		"confirmed_lsn": s.machine.ConfirmedLSN(),
		"restart_lsn":   s.machine.RestartLSN(),
		"reason": fmt.Sprintf(
			"confirm at commit boundary; restart=min(confirmed, retained txn starts)%s",
			changedSuffix(before, s.machine.RestartLSN())),
	})
	return nil
}

// compactLocked 在重启位点超过当前 WAL 保留起点时重写 WAL，
// 回收 LSN < restartLSN 的记录。重写采用临时文件 + rename + fsync。
func (s *Slot) compactLocked(ctx context.Context) error {
	restart := s.machine.RestartLSN()
	if restart <= s.compactedLSN {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	retained := s.machine.retainedLog()
	tmp, err := os.CreateTemp(s.dir, ".wal-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	for _, r := range retained {
		if _, err := tmp.Write(encodeRecord(r)); err != nil {
			tmp.Close()
			os.Remove(tmpName)
			return err
		}
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	finalName := filepath.Join(s.dir, walName)
	if err := os.Rename(tmpName, finalName); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := syncDir(s.dir); err != nil {
		return err
	}
	f, err := os.OpenFile(finalName, os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	old := s.wal
	s.wal = f
	s.compactedLSN = restart
	_ = old.Close()
	return nil
}

// Reemitted 返回崩溃重启后需要重新发给消费端的已提交未确认事务，
// 按提交 LSN 升序，确保不重不漏。
func (s *Slot) Reemitted() []Transaction {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.machine.PendingTransactions()
}

// ConfirmedLSN 返回确认位点。
func (s *Slot) ConfirmedLSN() uint64 {
	restart, confirmed := s.Positions()
	_ = restart
	return confirmed
}

// RestartLSN 返回重启位点。
func (s *Slot) RestartLSN() uint64 {
	restart, _ := s.Positions()
	return restart
}

// Positions 原子读取两个位点；始终满足 restart <= confirmed。
func (s *Slot) Positions() (restart, confirmed uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.machine.RestartLSN(), s.machine.ConfirmedLSN()
}

// NextLSN 返回下一条日志记录应当使用的 LSN（崩溃恢复后同样连续）。
func (s *Slot) NextLSN() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.machine.NextLSN()
}

// Close 关闭复制槽。
func (s *Slot) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.wal.Close()
}

func (s *Slot) appendReason(r Record, committed bool, beforeRestart uint64) string {
	switch r.Kind {
	case KindCommit:
		return "commit seen; whole transaction emitted atomically" +
			changedSuffix(beforeRestart, s.machine.RestartLSN())
	case KindAbort:
		return "abort seen; buffered transaction records dropped, never emitted" +
			changedSuffix(beforeRestart, s.machine.RestartLSN())
	default:
		return "record buffered into in-progress transaction" +
			changedSuffix(beforeRestart, s.machine.RestartLSN())
	}
}

func changedSuffix(before, after uint64) string {
	if after > before {
		return fmt.Sprintf("; restart advanced %d->%d and older log reclaimed", before, after)
	}
	return ""
}

func (s *Slot) logReject(op string, r Record, err error) {
	s.logLine(op+"_rejected", map[string]any{
		"record":        r,
		"confirmed_lsn": s.machine.ConfirmedLSN(),
		"restart_lsn":   s.machine.RestartLSN(),
		"emitted":       nil,
		"reason":        err.Error(),
	})
}

func (s *Slot) logLine(event string, fields map[string]any) {
	entry := map[string]any{
		"ts":     time.Now().Format(time.RFC3339Nano),
		"event":  event,
		"slot":   s.dir,
		"fields": fields,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	_, _ = s.logWriter.Write(append(data, '\n'))
}
