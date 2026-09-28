// Package transaction 把交错到达的变更事务按事务边界重组。
//
// 多个事务的行可能交错到达，Reassembler 按事务缓冲这些行：
// 提交时按行的到达顺序整体输出该事务的完整行集（空事务也输出），
// 回滚时丢弃该事务缓冲的全部行。输出顺序即提交事件的到达顺序。
//
// Reassembler 的所有方法均可被并发调用；每个事务的缓冲与输出在内部锁下
// 原子地完成，因此同一输入序列无论怎样并发交错，输出内容与顺序都确定不变。
package transaction

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// Row 是一次写入携带的数据行。行的顺序由 Write 的调用（到达）顺序决定。
type Row struct {
	Key   string
	Value string
}

// EventType 是事件种类。
type EventType int

const (
	// EventBegin 开启一个事务。
	EventBegin EventType = iota + 1
	// EventWrite 向一个进行中的事务写入一行。
	EventWrite
	// EventCommit 提交一个进行中的事务并整体输出。
	EventCommit
	// EventRollback 回滚一个进行中的事务并丢弃其全部行。
	EventRollback
)

// RejectReason 是操作被拒绝的可区分原因。
type RejectReason string

const (
	// ReasonInvalidEvent 非法事件：未知的事件类型。
	ReasonInvalidEvent RejectReason = "invalid_event"
	// ReasonEmptyTxID 非法事件：事务标识为空。
	ReasonEmptyTxID RejectReason = "empty_tx_id"
	// ReasonDuplicateBegin 重复开始：同名事务已在进行中。
	ReasonDuplicateBegin RejectReason = "duplicate_begin"
	// ReasonTxNotFound 目标事务不在进行中（写入/提交/回滚了未开始或已结束的事务）。
	ReasonTxNotFound RejectReason = "tx_not_found"
	// ReasonBufferLimitExceeded 缓冲超限：本次写入会使进行中事务的总缓冲行数超过上限。
	ReasonBufferLimitExceeded RejectReason = "buffer_limit_exceeded"
)

// RejectError 携带被拒绝操作的原因与目标事务标识，
// 便于调用方按 Reason 区分处理，并用 errors.Is 匹配对应哨兵错误。
type RejectError struct {
	Reason RejectReason
	TxID   string
}

// 哨兵错误，供 errors.Is 判定具体拒绝原因。
var (
	ErrInvalidEvent        = &RejectError{Reason: ReasonInvalidEvent}
	ErrEmptyTxID           = &RejectError{Reason: ReasonEmptyTxID}
	ErrDuplicateBegin      = &RejectError{Reason: ReasonDuplicateBegin}
	ErrTxNotFound          = &RejectError{Reason: ReasonTxNotFound}
	ErrBufferLimitExceeded = &RejectError{Reason: ReasonBufferLimitExceeded}
)

// 保证 RejectError 实现 error 接口。
var _ error = (*RejectError)(nil)

// Error 实现 error。
func (e *RejectError) Error() string {
	if e.TxID == "" {
		return string(e.Reason)
	}
	return string(e.Reason) + ": tx=" + e.TxID
}

// Is 使 errors.Is 能按 Reason 匹配对应哨兵错误（忽略 TxID 差异）。
func (e *RejectError) Is(target error) bool {
	t, ok := target.(*RejectError)
	return ok && t.Reason == e.Reason
}

// Event 是事务生命周期中的一个事件。
type Event struct {
	Type EventType
	TxID string
	Row  Row // 仅 EventWrite 使用
}

// CommittedTx 是一个已提交事务的完整输出：按到达顺序排列的行集。
// Seq 为提交序号（从 1 开始单调递增），即提交事件的到达顺序。
// 提交空事务时 Rows 为长度 0 的非 nil 切片。
type CommittedTx struct {
	TxID string
	Seq  int64
	Rows []Row
}

// txn 是一个进行中事务的内部状态。
type txn struct {
	rows []Row
}

// Reassembler 按事务边界重组交错到达的变更。零值不可用，须用 NewReassembler 构造。
type Reassembler struct {
	mu          sync.Mutex
	maxBuffered int
	buffered    int
	txns        map[string]*txn
	commitSeq   int64
	log         io.Writer
}

// Option 配置 Reassembler。
type Option func(*Reassembler)

// WithLogger 把输入、输出与判定依据写入给定的日志目的地（每次调用一行）。
func WithLogger(w io.Writer) Option {
	return func(r *Reassembler) { r.log = w }
}

// NewReassembler 创建重组器。maxBufferedRows 为所有进行中事务缓冲行总数的上限，
// 必须大于 0，否则 panic（这是构造期编程错误，而非可拒绝的运行期事件）。
func NewReassembler(maxBufferedRows int, opts ...Option) *Reassembler {
	if maxBufferedRows <= 0 {
		panic("transaction: maxBufferedRows must be > 0, got " + strconv.Itoa(maxBufferedRows))
	}
	r := &Reassembler{
		maxBuffered: maxBufferedRows,
		txns:        make(map[string]*txn),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Begin 开启一个事务。空标识返回 ErrEmptyTxID；事务已在进行中返回 ErrDuplicateBegin。
func (r *Reassembler) Begin(txID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.logEvent("BEGIN", txID)
	if txID == "" {
		return r.rejectLocked("BEGIN", "", ErrEmptyTxID)
	}
	if _, exists := r.txns[txID]; exists {
		return r.rejectLocked("BEGIN", txID, ErrDuplicateBegin)
	}
	r.txns[txID] = &txn{rows: make([]Row, 0)}
	r.logOK("BEGIN", txID)
	return nil
}

// Write 向进行中的事务追加一行，行的到达顺序即追加顺序。
// 事务不在进行中返回 ErrTxNotFound；追加后总缓冲行数会超过上限时返回
// ErrBufferLimitExceeded，且该行与任何状态都不发生改变。
func (r *Reassembler) Write(txID string, row Row) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.logWrite(txID, row)
	if txID == "" {
		return r.rejectLocked("WRITE", "", ErrEmptyTxID)
	}
	t, ok := r.txns[txID]
	if !ok {
		return r.rejectLocked("WRITE", txID, ErrTxNotFound)
	}
	// 先判定后修改：超限时不得把行追加进缓冲。
	if r.buffered >= r.maxBuffered {
		return r.rejectLocked("WRITE", txID, ErrBufferLimitExceeded)
	}
	t.rows = append(t.rows, row)
	r.buffered++
	r.logOK("WRITE", txID)
	return nil
}

// Commit 提交事务：按行的到达顺序整体输出该事务（空事务也输出），并清空其缓冲。
// 事务不在进行中返回 ErrTxNotFound，且不产生任何输出。
// 返回的 Rows 切片为内部缓冲的独立拷贝，调用方后续读取不受其他操作影响。
func (r *Reassembler) Commit(txID string) (CommittedTx, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.logEvent("COMMIT", txID)
	if txID == "" {
		return CommittedTx{}, r.rejectLocked("COMMIT", "", ErrEmptyTxID)
	}
	t, ok := r.txns[txID]
	if !ok {
		return CommittedTx{}, r.rejectLocked("COMMIT", txID, ErrTxNotFound)
	}

	// 在锁内完成拷贝与记账，保证“发出的行”与“缓冲中的行”逐条一致，
	// 且删除事务后缓冲立即归零于该事务的份额。
	r.commitSeq++
	out := CommittedTx{TxID: txID, Seq: r.commitSeq, Rows: make([]Row, len(t.rows))}
	copy(out.Rows, t.rows)
	r.buffered -= len(t.rows)
	delete(r.txns, txID)
	r.logOut(out)
	return out, nil
}

// Rollback 回滚事务：丢弃其缓冲的全部行并释放缓冲份额。
// 事务不在进行中返回 ErrTxNotFound。
func (r *Reassembler) Rollback(txID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.logEvent("ROLLBACK", txID)
	if txID == "" {
		return r.rejectLocked("ROLLBACK", "", ErrEmptyTxID)
	}
	t, ok := r.txns[txID]
	if !ok {
		return r.rejectLocked("ROLLBACK", txID, ErrTxNotFound)
	}
	dropped := len(t.rows)
	r.buffered -= dropped
	delete(r.txns, txID)
	r.logRollback(txID, dropped)
	return nil
}

// Apply 处理单个事件。仅提交成功时返回非 nil 的 *CommittedTx；
// 事件类型未知返回 ErrInvalidEvent。被拒绝的事件不改变任何状态。
func (r *Reassembler) Apply(ev Event) (*CommittedTx, error) {
	switch ev.Type {
	case EventBegin:
		return nil, r.Begin(ev.TxID)
	case EventWrite:
		return nil, r.Write(ev.TxID, ev.Row)
	case EventCommit:
		out, err := r.Commit(ev.TxID)
		if err != nil {
			return nil, err
		}
		return &out, nil
	case EventRollback:
		return nil, r.Rollback(ev.TxID)
	default:
		r.mu.Lock()
		defer r.mu.Unlock()
		r.logf("in  EVENT#%d tx=%s\n", int(ev.Type), quote(ev.TxID))
		return nil, r.rejectLocked("APPLY", ev.TxID, ErrInvalidEvent)
	}
}

// BufferedRows 返回所有进行中事务当前缓冲的行总数。
func (r *Reassembler) BufferedRows() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buffered
}

// InFlight 返回当前进行中的事务数。
func (r *Reassembler) InFlight() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.txns)
}

// rejectLocked 用对应哨兵构造带事务标识的拒绝错误并写日志。调用时须持有 r.mu。
func (r *Reassembler) rejectLocked(op, txID string, sentinel *RejectError) error {
	err := &RejectError{Reason: sentinel.Reason, TxID: txID}
	r.logf("rej %s tx=%s reason=%s buffered=%d inflight=%d\n",
		op, quote(txID), err.Reason, r.buffered, len(r.txns))
	return err
}

// logEvent 记录一条不带数据行的输入事件（BEGIN/COMMIT/ROLLBACK）。
func (r *Reassembler) logEvent(op, txID string) {
	r.logf("in  %s tx=%s\n", op, quote(txID))
}

// logWrite 记录一条写入输入事件，始终附带行内容（即使 Key/Value 均为空）。
func (r *Reassembler) logWrite(txID string, row Row) {
	r.logf("in  WRITE tx=%s row=%s\n", quote(txID), formatRow(row))
}

// logOK 记录一条被接受但不产生输出的事件及判定后状态。
func (r *Reassembler) logOK(op, txID string) {
	r.logf("ok  %s tx=%s buffered=%d inflight=%d\n",
		op, quote(txID), r.buffered, len(r.txns))
}

// logOut 记录一次提交输出：序号、行内容及判定后缓冲状态。
func (r *Reassembler) logOut(out CommittedTx) {
	r.logf("out COMMIT tx=%s seq=%d rows=%d %sbuffered=%d inflight=%d\n",
		quote(out.TxID), out.Seq, len(out.Rows), formatRows(out.Rows), r.buffered, len(r.txns))
}

// logRollback 记录一次回滚：丢弃行数及判定后缓冲状态。
func (r *Reassembler) logRollback(txID string, dropped int) {
	r.logf("ok  ROLLBACK tx=%s dropped=%d buffered=%d inflight=%d\n",
		quote(txID), dropped, r.buffered, len(r.txns))
}

// logf 在配置了日志目的地时输出一行。
func (r *Reassembler) logf(format string, args ...any) {
	if r.log != nil {
		fmt.Fprintf(r.log, format, args...)
	}
}

// formatRows 格式化行列表，空事务显示为空括号对。
func formatRows(rows []Row) string {
	if len(rows) == 0 {
		return "[] "
	}
	parts := make([]string, len(rows))
	for i, row := range rows {
		parts[i] = formatRow(row)
	}
	return "[" + strings.Join(parts, " ") + "] "
}

// formatRow 格式化单行。
func formatRow(row Row) string {
	return quote(row.Key) + "=" + quote(row.Value)
}

// quote 用 %q 引用文本，使空串与空白在日志中可见。
func quote(s string) string {
	return strconv.Quote(s)
}
