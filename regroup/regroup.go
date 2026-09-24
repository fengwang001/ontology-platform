// Package regroup 按事务管理全部进行中事务、缓冲行数总账与输出日志。
// 依赖 txn，不依赖 api。
package regroup

import (
	"sync"

	"ontology/txn"
)

// R 是事务重组器。零值不可用，须用 New 构造。
type R struct {
	mu          sync.Mutex
	txs         map[int64]*txn.T // 所有 BEGIN 过的事务（含已结束）
	total       int              // 进行中事务缓冲行数总账
	maxRows     int
	out         []txn.Txn // 输出日志，按 COMMIT 到达顺序
	lastChecked int       // 最近一次 COMMIT/ROLLBACK 检查过的缓冲行个数（非导出）
}

// New 创建重组器，maxRows 为全部进行中事务缓冲行数合计的上限。
func New(maxRows int) *R {
	return &R{txs: make(map[int64]*txn.T), maxRows: maxRows}
}

// Feed 处理一条事件，返回其触发的输出（至多一条）。任何错误都不改变状态。
func (r *R) Feed(ev txn.Event) ([]txn.Txn, error) {
	if err := ev.Validate(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t, begun := r.txs[ev.Tx]
	active := begun && t.Status() == txn.Active
	switch ev.Kind {
	case txn.Begin:
		if begun {
			return nil, txn.ErrDuplicateBegin
		}
		r.txs[ev.Tx] = txn.New()
	case txn.Row:
		if !active {
			return nil, txn.ErrUnknownTx
		}
		if r.total+1 > r.maxRows {
			return nil, txn.ErrBufferFull
		}
		t.AddRow(ev.Data)
		r.total++
	case txn.Commit:
		if !active {
			return nil, txn.ErrUnknownTx
		}
		rows := t.Commit()
		r.total -= len(rows)
		r.lastChecked = len(rows)
		out := txn.Txn{Tx: ev.Tx, Rows: rows}
		r.out = append(r.out, out)
		return []txn.Txn{out}, nil
	case txn.Rollback:
		if !active {
			return nil, txn.ErrUnknownTx
		}
		n := t.Rollback()
		r.total -= n
		r.lastChecked = n
	}
	return nil, nil
}

// Output 返回迄今输出的全部事务（按 COMMIT 顺序）。
func (r *R) Output() []txn.Txn {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]txn.Txn, len(r.out))
	copy(out, r.out)
	return out
}

// Buffered 返回当前全部进行中事务的缓冲行数合计。
func (r *R) Buffered() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.total
}
