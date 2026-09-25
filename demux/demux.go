// Package demux 实现帧分派：Recv 的投递/半开/陈旧判定，
// Open/Close/Send 的世代校验。依赖 slot，不依赖 api。
package demux

import (
	"errors"

	"ontology/slot"
)

// 四类可判定哨兵错误，互不相同。
var (
	ErrNoSlots  = slot.ErrNoSlots                // Open 时无空闲槽
	ErrBadID    = errors.New("demux: bad id")    // connID 不在 [0,C)
	ErrHalfOpen = errors.New("demux: half-open") // 帧/操作指向 FREE 槽
	ErrStale    = errors.New("demux: stale gen") // gen 与当前世代不符
)

// Engine 是分派器：槽表 + 每连接累积数据。非并发安全，由上层加锁。
type Engine struct {
	t    *slot.Table
	data [][]byte // 每 connID 的累积数据，仅当该槽 OPEN 时有意义
}

// New 建一个 C 槽分派器。
func New(C int) *Engine {
	return &Engine{t: slot.New(C), data: make([][]byte, C)}
}

// Open 分配最小空闲槽，世代递增，返回句柄。
func (e *Engine) Open() (slot.Handle, error) {
	h, err := e.t.Open()
	if err != nil {
		return slot.Handle{}, err
	}
	e.data[h.ID] = e.data[h.ID][:0] // 复用槽时清空旧数据
	return h, nil
}

// Close 要求句柄世代匹配；成功后槽回 FREE 并清空累积数据。
func (e *Engine) Close(h slot.Handle) error {
	if err := e.checkHandle(h); err != nil {
		return err
	}
	e.t.Close(h)
	e.data[h.ID] = nil
	return nil
}

// Send 校验句柄世代；本机制不真正发帧，数据不落任何状态。
func (e *Engine) Send(h slot.Handle, data []byte) error {
	return e.checkHandle(h)
}

// Recv 收到一帧：超范围→ErrBadID；FREE→ErrHalfOpen；
// gen 不匹配→ErrStale；匹配→追加到该连接累积数据。
func (e *Engine) Recv(id, gen int, data []byte) error {
	open, cur, ok := e.t.State(id)
	if !ok {
		return ErrBadID
	}
	if !open {
		return ErrHalfOpen
	}
	if gen != cur {
		return ErrStale
	}
	e.data[id] = append(e.data[id], data...)
	return nil
}

// Data 返回某连接的累积数据副本；id 越界或 FREE 返回 nil。
func (e *Engine) Data(id int) []byte {
	open, _, ok := e.t.State(id)
	if !ok || !open {
		return nil
	}
	return append([]byte(nil), e.data[id]...)
}

// Gen 返回某槽当前世代；id 越界返回 -1。
func (e *Engine) Gen(id int) int {
	_, gen, ok := e.t.State(id)
	if !ok {
		return -1
	}
	return gen
}

// CheckOpenCost 透传槽表的 Open 定位代价核验。
func (e *Engine) CheckOpenCost() error { return e.t.CheckOpenCost() }

// checkHandle 校验句柄：越界→ErrBadID，FREE→ErrHalfOpen，世代不符→ErrStale。
// 所有错误路径都在任何状态变更之前返回（失败不留痕）。
func (e *Engine) checkHandle(h slot.Handle) error {
	open, cur, ok := e.t.State(h.ID)
	if !ok {
		return ErrBadID
	}
	if !open {
		return ErrHalfOpen
	}
	if cur != h.Gen {
		return ErrStale
	}
	return nil
}
