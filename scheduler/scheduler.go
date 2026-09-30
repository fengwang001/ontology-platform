package scheduler

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
)

// AbortReason 描述事务被中止的原因。
type AbortReason string

const (
	// AbortReadLate 「读过晚」：事务时间戳小于键当前的写时间戳。
	AbortReadLate AbortReason = "读过晚"
	// AbortWriteLate 「写过晚」：事务时间戳小于键当前的读时间戳。
	AbortWriteLate AbortReason = "写过晚"
)

// 被整体拒绝（不改变任何状态）时返回的哨兵错误。
var (
	// ErrEmptyKey 键为空，校验顺序排在第一位。
	ErrEmptyKey = errors.New("键为空")
	// ErrTxNotFound 事务不存在（从未 Begin）。
	ErrTxNotFound = errors.New("事务不存在")
	// ErrTxCommitted 事务已经提交。
	ErrTxCommitted = errors.New("事务已提交")
	// ErrTxAborted 事务已经中止。
	ErrTxAborted = errors.New("事务已中止")
)

// AbortError 表示一次合法但导致事务中止的结果。
type AbortError struct {
	Reason AbortReason
}

func (e *AbortError) Error() string { return string(e.Reason) }

// txState 是事务的生命周期状态。
type txState int

const (
	txActive txState = iota
	txCommitted
	txAborted
)

// keyEntry 记录一个键的读时间戳、写时间戳与当前值。
type keyEntry struct {
	readTS  int64
	writeTS int64
	value   int64
}

type transaction struct {
	ts     int64
	status txState
	reason AbortReason
	// buffer 是事务私有写缓冲；读自己写过时直接取这里。
	buffer map[string]int64
}

// Scheduler 是基于时间戳排序（TO）的事务调度器。
//
// 单把互斥锁串行化所有内部状态变更：每个方法在锁内完成全部检查与
// 修改，因此并发调用的效果等价于这些调用按某个先后顺序串行执行，
// 相同操作序列重放结果完全相同。
type Scheduler struct {
	mu     sync.Mutex
	nextTS int64
	keys   map[string]*keyEntry
	txs    map[int64]*transaction
	log    io.Writer
}

// New 创建一个空调度器，日志默认写到标准错误。
func New() *Scheduler {
	return &Scheduler{
		nextTS: 1,
		keys:   make(map[string]*keyEntry),
		txs:    make(map[int64]*transaction),
		log:    os.Stderr,
	}
}

// NewWithLogger 创建调度器并指定日志输出位置（传 nil 关闭日志）。
func NewWithLogger(w io.Writer) *Scheduler {
	s := New()
	s.log = w
	return s
}

func (s *Scheduler) logf(format string, args ...any) {
	if s.log != nil {
		fmt.Fprintln(s.log, strings.TrimSpace(fmt.Sprintf(format, args...)))
	}
}

// Begin 开启一个新事务，返回其开始时间戳（从 1 起连续递增）。
func (s *Scheduler) Begin() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	ts := s.nextTS
	s.nextTS++
	s.txs[ts] = &transaction{ts: ts, buffer: make(map[string]int64)}
	s.logf("输入: BEGIN -> 分配时间戳 %d；判定依据: 时间戳从 1 起连续递增", ts)
	return ts
}

// checkOperable 按「键为空 → 事务不存在 → 已提交 → 已中止」的顺序
// 只报第一个错误。调用方必须持有 s.mu。
func (s *Scheduler) checkOperable(ts int64, key string) (*transaction, *keyEntry, error) {
	if key == "" {
		return nil, nil, ErrEmptyKey
	}
	tx, ok := s.txs[ts]
	if !ok {
		return nil, nil, ErrTxNotFound
	}
	switch tx.status {
	case txCommitted:
		return nil, nil, ErrTxCommitted
	case txAborted:
		return nil, nil, ErrTxAborted
	}
	return tx, s.key(key), nil
}

// key 返回键对象；不存在则惰性创建（读/写时间戳与值初值均为 0）。
func (s *Scheduler) key(name string) *keyEntry {
	k, ok := s.keys[name]
	if !ok {
		k = &keyEntry{}
		s.keys[name] = k
	}
	return k
}

// abort 把事务转为已中止并丢弃缓冲。调用方必须持有 s.mu。
func (s *Scheduler) abort(tx *transaction, reason AbortReason) *AbortError {
	tx.status = txAborted
	tx.reason = reason
	tx.buffer = nil
	s.logf("判定依据: 事务 ts=%d %s（ts=%d < 键时间戳），事务转为已中止并丢弃缓冲",
		tx.ts, reason, tx.ts)
	return &AbortError{Reason: reason}
}

// Read 执行事务读。
//
//   - 私有写缓冲里已有该键：返回缓冲值，不触碰键的任何记录；
//   - 事务时间戳 < 键写时间戳：事务中止，原因「读过晚」；
//   - 否则返回当前值，并把读时间戳抬到 max(读时间戳, 事务时间戳)。
func (s *Scheduler) Read(ts int64, key string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, k, err := s.checkOperable(ts, key)
	if err != nil {
		s.logf("输入: READ ts=%d key=%q -> 拒绝: %v（状态不变）", ts, key, err)
		return 0, err
	}

	if v, ok := tx.buffer[key]; ok {
		s.logf("输入: READ ts=%d key=%q -> 输出 %d；判定依据: 读自己缓冲的写，不触碰键记录",
			ts, key, v)
		return v, nil
	}

	if tx.ts < k.writeTS {
		s.logf("输入: READ ts=%d key=%q -> 中止 %s；判定依据: ts=%d < 写时间戳 WTS=%d（更晚事务已安装值）",
			ts, key, AbortReadLate, tx.ts, k.writeTS)
		return 0, s.abort(tx, AbortReadLate)
	}

	value := k.value
	old := k.readTS
	if tx.ts > k.readTS {
		k.readTS = tx.ts
	}
	s.logf("输入: READ ts=%d key=%q -> 输出 %d；判定依据: ts=%d >= WTS=%d，RTS %d->%d（取较大者）",
		ts, key, value, tx.ts, k.writeTS, old, k.readTS)
	return value, nil
}

// Write 只把写暂存进事务私有缓冲。
//
// 若事务时间戳 < 键读时间戳，事务立即中止，原因「写过晚」；
// 恰相等时不中止（比较是严格小于）。
func (s *Scheduler) Write(ts int64, key string, value int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, k, err := s.checkOperable(ts, key)
	if err != nil {
		s.logf("输入: WRITE ts=%d key=%q value=%d -> 拒绝: %v（状态不变）",
			ts, key, value, err)
		return err
	}

	if tx.ts < k.readTS {
		s.logf("输入: WRITE ts=%d key=%q value=%d -> 中止 %s；判定依据: ts=%d < 读时间戳 RTS=%d（已有更晚读者）",
			ts, key, value, AbortWriteLate, tx.ts, k.readTS)
		return s.abort(tx, AbortWriteLate)
	}

	tx.buffer[key] = value
	s.logf("输入: WRITE ts=%d key=%q value=%d -> 已暂存私有缓冲；判定依据: ts=%d >= RTS=%d（相等亦合法），不立即安装",
		ts, key, value, tx.ts, k.readTS)
	return nil
}

// Commit 先按键升序逐个复查缓冲写，全部通过后再逐个安装。
//
//   - 复查时任一键 ts < RTS：整个事务中止「写过晚」，不安装任何写；
//   - 安装时 ts < WTS：该写被忽略（Thomas 写规则），值与写时间戳都不变；
//   - 否则安装值并把写时间戳置为 ts（读时间戳不变）。
func (s *Scheduler) Commit(ts int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.txs[ts]; !ok {
		s.logf("输入: COMMIT ts=%d -> 拒绝: %v（状态不变）", ts, ErrTxNotFound)
		return ErrTxNotFound
	}
	tx := s.txs[ts]
	switch tx.status {
	case txCommitted:
		s.logf("输入: COMMIT ts=%d -> 拒绝: %v（状态不变）", ts, ErrTxCommitted)
		return ErrTxCommitted
	case txAborted:
		s.logf("输入: COMMIT ts=%d -> 拒绝: %v（状态不变）", ts, ErrTxAborted)
		return ErrTxAborted
	}

	names := make([]string, 0, len(tx.buffer))
	for name := range tx.buffer {
		names = append(names, name)
	}
	sort.Strings(names)

	// 第一阶段：按键升序复查；任一失败则零安装。
	for _, name := range names {
		k := s.key(name)
		if tx.ts < k.readTS {
			s.logf("输入: COMMIT ts=%d -> 中止 %s；判定依据: 复查 key=%q 时 ts=%d < RTS=%d，全部缓冲写零安装并丢弃",
				ts, AbortWriteLate, name, tx.ts, k.readTS)
			return s.abort(tx, AbortWriteLate)
		}
	}

	// 第二阶段：全部通过后逐个安装。
	for _, name := range names {
		k := s.key(name)
		v := tx.buffer[name]
		if tx.ts < k.writeTS {
			s.logf("判定依据: 安装 key=%q 时 ts=%d < WTS=%d，该写（值 %d）被忽略，值与 WTS 均不变",
				name, tx.ts, k.writeTS, v)
			continue
		}
		k.value = v
		k.writeTS = tx.ts
		s.logf("判定依据: 安装 key=%q 值 %d，WTS->%d（ts=%d >= 旧 WTS）",
			name, v, k.writeTS, tx.ts)
	}

	tx.status = txCommitted
	tx.buffer = nil
	s.logf("输入: COMMIT ts=%d -> 已提交", ts)
	return nil
}

// Status 返回事务状态（"active"/"committed"/"aborted"）与中止原因；
// 未中止或拒绝时原因为空串。事务不存在返回 ErrTxNotFound。
func (s *Scheduler) Status(ts int64) (string, AbortReason, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, ok := s.txs[ts]
	if !ok {
		return "", "", ErrTxNotFound
	}
	switch tx.status {
	case txActive:
		return "active", "", nil
	case txCommitted:
		return "committed", "", nil
	default:
		return "aborted", tx.reason, nil
	}
}

// Inspect 返回键当前的读时间戳、写时间戳与值；从未建立的键返回零值状态。
func (s *Scheduler) Inspect(key string) KeyState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if k, ok := s.keys[key]; ok {
		return KeyState{ReadTS: k.readTS, WriteTS: k.writeTS, Value: k.value}
	}
	return KeyState{}
}

// KeyState 描述一个键的对外可见状态。
type KeyState struct {
	ReadTS  int64
	WriteTS int64
	Value   int64
}
