// Package obx 实现事务发件箱表：id/csn 分配、事务开启/提交/中止、
// 已提交未标记消息的有序待投队列与标记。不依赖其他包。
package obx

import (
	"errors"
	"sync"
)

// 三类可判定哨兵错误，互不相同。
var (
	ErrTxUnavailable = errors.New("obx: transaction unavailable")
	ErrEmptyPayload  = errors.New("obx: empty payload")
	ErrBacklog       = errors.New("obx: pending backlog exceeds limit")
)

// Msg 是发件箱里的一条消息。ID 写入时全局自增，CSN 提交时全局自增。
type Msg struct {
	ID      int
	CSN     int
	Payload string
}

type txState int

const (
	txOpen txState = iota
	txCommitted
	txAborted
)

type tx struct {
	msgs  []Msg
	state txState
}

type row struct {
	msg    Msg
	marked bool
}

// Box 是发件箱表。rows 是已提交消息的全量表（含已标记），
// pending 是已提交未标记的待投队列，按 (csn, id) 有序。
type Box struct {
	mu         sync.Mutex
	maxPending int
	nextID     int
	nextCSN    int
	txs        map[string]*tx
	rows       []row
	pending    []Msg
	checked    int // 最近一次 Take 检查过的消息条数（非导出，不进公开接口）
}

func New(maxPending int) *Box {
	return &Box{maxPending: maxPending, txs: make(map[string]*tx)}
}

// Write 向事务 tx 写一条消息；事务名首次出现时自动开启。
// 写入时分配全局自增 id，被中止事务用掉的 id 不回收。
func (b *Box) Write(txName, payload string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if payload == "" {
		return ErrEmptyPayload
	}
	t, ok := b.txs[txName]
	if ok && t.state != txOpen {
		return ErrTxUnavailable
	}
	if !ok {
		t = &tx{}
		b.txs[txName] = t
	}
	b.nextID++
	t.msgs = append(t.msgs, Msg{ID: b.nextID, Payload: payload})
	return nil
}

// Commit 提交事务：分配 csn，全部消息进入待投队列。
// 提交后已提交未标记数超 maxPending 则整体拒绝，不消耗 csn，事务保持开启。
func (b *Box) Commit(txName string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	t, ok := b.txs[txName]
	if !ok || t.state != txOpen {
		return ErrTxUnavailable
	}
	if len(b.pending)+len(t.msgs) > b.maxPending {
		return ErrBacklog
	}
	b.nextCSN++
	for i := range t.msgs {
		t.msgs[i].CSN = b.nextCSN
	}
	b.pending = append(b.pending, t.msgs...)
	for _, m := range t.msgs {
		b.rows = append(b.rows, row{msg: m})
	}
	t.state = txCommitted
	return nil
}

// Abort 中止事务：丢弃其全部消息（已用 id 不回收）。
func (b *Box) Abort(txName string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	t, ok := b.txs[txName]
	if !ok || t.state != txOpen {
		return ErrTxUnavailable
	}
	t.msgs = nil
	t.state = txAborted
	return nil
}

// Take 取出全部已提交未标记消息，按 (csn, id) 有序；不修改队列。
// checked 只统计本次检查过的条数，与已标记消息总量无关。
func (b *Box) Take() []Msg {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.checked = len(b.pending)
	out := make([]Msg, len(b.pending))
	copy(out, b.pending)
	return out
}

// Mark 把一条消息标记为已投递（从待投队列移除，表中置标记位）。
func (b *Box) Mark(id int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, m := range b.pending {
		if m.ID == id {
			b.pending = append(b.pending[:i], b.pending[i+1:]...)
			break
		}
	}
	for i := range b.rows {
		if b.rows[i].msg.ID == id {
			b.rows[i].marked = true
			return
		}
	}
}
