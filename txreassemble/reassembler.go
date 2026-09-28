package txreassemble

import (
	"fmt"
	"strings"
	"sync"
)

// Logger 是重组器用于打印输入、输出与判定依据的最小日志接口，
// 标准库 *log.Logger 天然满足它。nil 表示不打印日志。
type Logger interface {
	Printf(format string, args ...any)
}

// Reassembler 按事务边界重组交错到达的事件。
// 所有进行中事务缓冲的总行数受 maxRows 限制；并发安全。
type Reassembler struct {
	mu           sync.Mutex
	maxRows      int
	bufferedRows int
	active       map[string]*txnState
	committed    []CommittedTx
	commitSeq    int64
	logger       Logger
}

// txnState 是单个进行中事务的缓冲状态。
type txnState struct {
	// rows 用非 nil 切片初始化，保证空事务提交后输出为空数组而非 null。
	rows []Row
}

// New 创建缓冲总行数上限为 maxBufferedRows 的重组器。
// maxBufferedRows 为 0 时任何 Write 都会以 RejectBufferFull 被拒绝，
// 但空事务仍可提交。
func New(maxBufferedRows int) *Reassembler {
	if maxBufferedRows < 0 {
		panic("txreassemble: maxBufferedRows must be >= 0")
	}
	return &Reassembler{
		maxRows: maxBufferedRows,
		active:  make(map[string]*txnState),
	}
}

// WithLogger 设置日志器并返回重组器本身，便于链式构造：
//
//	r := txreassemble.New(100).WithLogger(log.New(os.Stderr, "", log.LstdFlags))
//
// 日志在决策完成、锁释放之后打印，因此日志回调不会持锁。
func (r *Reassembler) WithLogger(l Logger) *Reassembler {
	r.mu.Lock()
	r.logger = l
	r.mu.Unlock()
	return r
}

// Begin 开始一个事务。
// 空 txID 返回 RejectInvalidEvent；事务已在进行中返回 RejectDuplicateBegin。
func (r *Reassembler) Begin(txID string) error {
	var decision string
	err := func() error {
		r.mu.Lock()
		defer r.mu.Unlock()
		if strings.TrimSpace(txID) == "" {
			decision = "reject:invalid_event:empty tx id"
			return newReject(RejectInvalidEvent, txID, "begin with empty tx id")
		}
		if _, ok := r.active[txID]; ok {
			decision = "reject:duplicate_begin"
			return newReject(RejectDuplicateBegin, txID, "transaction already active")
		}
		r.active[txID] = &txnState{rows: make([]Row, 0)}
		decision = "accept"
		return nil
	}()
	r.logf("input=begin tx=%q decision=%s", txID, decision)
	return err
}

// Write 向进行中的事务追加一行，行在事务内严格按到达顺序保留。
// 空 txID 或零值行返回 RejectInvalidEvent；事务不在进行中返回
// RejectTxNotActive；会使总缓冲行数超过上限返回 RejectBufferFull。
// 被拒绝时缓冲与事务状态不变。
func (r *Reassembler) Write(txID string, row Row) error {
	var decision string
	err := func() error {
		r.mu.Lock()
		defer r.mu.Unlock()
		if strings.TrimSpace(txID) == "" {
			decision = "reject:invalid_event:empty tx id"
			return newReject(RejectInvalidEvent, txID, "write with empty tx id")
		}
		if !validRow(row) {
			decision = "reject:invalid_event:empty row"
			return newReject(RejectInvalidEvent, txID, "write with empty row (key and payload both empty)")
		}
		st, ok := r.active[txID]
		if !ok {
			decision = "reject:tx_not_active"
			return newReject(RejectTxNotActive, txID, "write to transaction that is not active")
		}
		if r.bufferedRows >= r.maxRows {
			decision = fmt.Sprintf("reject:buffer_full:buffered=%d,max=%d", r.bufferedRows, r.maxRows)
			return newReject(RejectBufferFull, txID, fmt.Sprintf("buffer full: %d/%d rows buffered", r.bufferedRows, r.maxRows))
		}
		st.rows = append(st.rows, row)
		r.bufferedRows++
		decision = fmt.Sprintf("accept:buffered=%d,tx_rows=%d", r.bufferedRows, len(st.rows))
		return nil
	}()
	r.logf("input=write tx=%q row=%q decision=%s", txID, row.Key, decision)
	return err
}

// Commit 提交事务：立即按行的到达顺序输出整个事务（空事务也输出），
// 输出顺序即提交的到达顺序。提交后该事务离开进行中集合，其缓冲行数释放。
// 空 txID 返回 RejectInvalidEvent；事务不在进行中返回 RejectTxNotActive。
func (r *Reassembler) Commit(txID string) (CommittedTx, error) {
	var out CommittedTx
	var decision string
	err := func() error {
		r.mu.Lock()
		defer r.mu.Unlock()
		if strings.TrimSpace(txID) == "" {
			decision = "reject:invalid_event:empty tx id"
			return newReject(RejectInvalidEvent, txID, "commit with empty tx id")
		}
		st, ok := r.active[txID]
		if !ok {
			decision = "reject:tx_not_active"
			return newReject(RejectTxNotActive, txID, "commit transaction that is not active")
		}
		delete(r.active, txID)
		r.bufferedRows -= len(st.rows)
		r.commitSeq++
		out = CommittedTx{TxID: txID, CommitSeq: r.commitSeq, Rows: st.rows}
		// 内部输出序列保留独立拷贝，与调用者拿到的切片互不影响。
		r.committed = append(r.committed, CommittedTx{
			TxID:      txID,
			CommitSeq: r.commitSeq,
			Rows:      cloneRows(st.rows),
		})
		decision = fmt.Sprintf("accept:output seq=%d rows=%d buffered=%d", r.commitSeq, len(st.rows), r.bufferedRows)
		return nil
	}()
	if err != nil {
		r.logf("input=commit tx=%q decision=%s", txID, decision)
		return CommittedTx{}, err
	}
	r.logf("input=commit tx=%q decision=%s output_tx=%q output_rows=%d", txID, decision, out.TxID, len(out.Rows))
	return out, nil
}

// Rollback 回滚事务：丢弃其全部缓冲行，事务离开进行中集合，不产生任何输出。
// 空 txID 返回 RejectInvalidEvent；事务不在进行中返回 RejectTxNotActive。
func (r *Reassembler) Rollback(txID string) error {
	var decision string
	err := func() error {
		r.mu.Lock()
		defer r.mu.Unlock()
		if strings.TrimSpace(txID) == "" {
			decision = "reject:invalid_event:empty tx id"
			return newReject(RejectInvalidEvent, txID, "rollback with empty tx id")
		}
		st, ok := r.active[txID]
		if !ok {
			decision = "reject:tx_not_active"
			return newReject(RejectTxNotActive, txID, "rollback transaction that is not active")
		}
		delete(r.active, txID)
		r.bufferedRows -= len(st.rows)
		decision = fmt.Sprintf("accept:discarded_rows=%d buffered=%d", len(st.rows), r.bufferedRows)
		return nil
	}()
	r.logf("input=rollback tx=%q decision=%s", txID, decision)
	return err
}

// Apply 按事件类型分发到 Begin/Write/Commit/Rollback。
// 未知事件类型（含 EventUnknown）以 RejectInvalidEvent 拒绝，且不改变任何状态。
// Commit 事件成功时返回非空 *CommittedTx，其余成功事件返回 nil。
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
		r.logf("input=unknown type=%d tx=%q decision=reject:invalid_event:unknown event type", ev.Type, ev.TxID)
		return nil, newReject(RejectInvalidEvent, ev.TxID, fmt.Sprintf("unknown event type %d", ev.Type))
	}
}

// Output 返回已提交事务输出序列的深拷贝快照，顺序即提交到达顺序。
// 返回切片及其 Row/Payload 均为独立拷贝，调用方修改不影响重组器。
func (r *Reassembler) Output() []CommittedTx {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]CommittedTx, len(r.committed))
	for i, tx := range r.committed {
		out[i] = CommittedTx{TxID: tx.TxID, CommitSeq: tx.CommitSeq, Rows: cloneRows(tx.Rows)}
	}
	return out
}

// BufferedRows 返回所有进行中事务当前缓冲的总行数。
func (r *Reassembler) BufferedRows() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.bufferedRows
}

// ActiveCount 返回进行中的事务数。
func (r *Reassembler) ActiveCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.active)
}

func (r *Reassembler) logf(format string, args ...any) {
	r.mu.Lock()
	l := r.logger
	r.mu.Unlock()
	if l != nil {
		l.Printf(format, args...)
	}
}

// validRow 判定一行是否为有效写入：Key 或 Payload 至少有一个非空。
func validRow(row Row) bool {
	return row.Key != "" || len(row.Payload) > 0
}

// cloneRows 返回 rows 的深拷贝（含 Payload 底层数组）；nil 输入返回非 nil 空切片。
func cloneRows(rows []Row) []Row {
	cp := make([]Row, len(rows))
	for i, row := range rows {
		cp[i] = Row{Key: row.Key}
		if row.Payload != nil {
			cp[i].Payload = append([]byte(nil), row.Payload...)
		}
	}
	return cp
}
