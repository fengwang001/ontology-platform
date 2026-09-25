// Package handshake 实现被动方三次握手的事件判定：
// RecvSYN 的新建/重传/替换，RecvACK 的完成/重复/半开/坏 ack。
// 状态存取委托给 hs 包，本包用互斥锁串行化所有操作。
package handshake

import (
	"errors"
	"sync"

	"ontology/hs"
)

// 可判定的哨兵错误，三者互不相同。
var (
	ErrBadAck   = errors.New("handshake: ack 不等于 serverISN+1")
	ErrHalfOpen = errors.New("handshake: 未知 src 的孤儿 ACK")
	ErrBadSeq   = errors.New("handshake: SYN 序号为负")
)

// Handler 是握手事件处理器，可并发使用。
type Handler struct {
	mu sync.Mutex
	s  *hs.Server
}

// New 返回空 Handler。
func New() *Handler { return &Handler{s: hs.New()} }

// RecvSYN 处理 SYN，返回 SYN-ACK 的 (serverISN, ack=seq+1)。
func (h *Handler) RecvSYN(src string, seq int64) (serverISN, ack int64, err error) {
	if seq < 0 { // 坏序号：写状态前拒绝，不留痕
		return 0, 0, ErrBadSeq
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if c, ok := h.s.LookupHalf(src); ok {
		if c.ClientISN == seq { // 重复 SYN：原样重发，不消耗 nextISN、不改状态
			return c.ServerISN, c.ClientISN + 1, nil
		}
		// 新连接尝试：替换半开连接
		isn := h.s.Alloc()
		h.s.PutHalf(src, hs.Conn{ClientISN: seq, ServerISN: isn})
		return isn, seq + 1, nil
	}
	// 新建半开连接
	isn := h.s.Alloc()
	h.s.PutHalf(src, hs.Conn{ClientISN: seq, ServerISN: isn})
	return isn, seq + 1, nil
}

// RecvACK 处理 ACK，握手完成时返回该连接的 (clientISN, serverISN)。
func (h *Handler) RecvACK(src string, ack int64) (clientISN, serverISN int64, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if c, ok := h.s.LookupHalf(src); ok {
		if ack != c.ServerISN+1 { // 坏 ACK：写状态前拒绝，不留痕
			return 0, 0, ErrBadAck
		}
		h.s.DeleteHalf(src)
		h.s.PutEst(src, c)
		return c.ClientISN, c.ServerISN, nil
	}
	if c, ok := h.s.LookupEst(src); ok { // 重复 ACK：幂等 no-op
		return c.ClientISN, c.ServerISN, nil
	}
	return 0, 0, ErrHalfOpen
}

// Established 报告 src 是否已完成握手。
func (h *Handler) Established(src string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.s.LookupEst(src)
	return ok
}

// NextISN 返回下一个将分配的 serverISN。
func (h *Handler) NextISN() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.s.NextISN()
}
