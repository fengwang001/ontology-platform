// Package api 是对外门面：新建 server、收发 SYN/ACK、查询已建立、自检。
package api

import (
	"errors"
	"fmt"

	"ontology/handshake"
)

// 哨兵错误，与 handshake 包同源，可用 errors.Is 判定。
var (
	ErrBadAck   = handshake.ErrBadAck
	ErrHalfOpen = handshake.ErrHalfOpen
	ErrBadSeq   = handshake.ErrBadSeq
)

// Server 是可靠传输握手的服务端，可并发使用。
type Server struct {
	h *handshake.Handler
}

// New 返回空 Server。
func New() *Server { return &Server{h: handshake.New()} }

// RecvSYN 处理 SYN，返回 SYN-ACK 的 (serverISN, ack=seq+1)。
func (s *Server) RecvSYN(src string, seq int64) (serverISN, ack int64, err error) {
	return s.h.RecvSYN(src, seq)
}

// RecvACK 处理 ACK，握手完成时返回该连接的 (clientISN, serverISN)。
func (s *Server) RecvACK(src string, ack int64) (clientISN, serverISN int64, err error) {
	return s.h.RecvACK(src, ack)
}

// Established 报告 src 是否已完成握手。
func (s *Server) Established(src string) bool { return s.h.Established(src) }

// SelfCheck 在独立的内部实例上跑内置握手序列，核验四条不变量。
// 不触碰调用者状态，可并发调用；全部通过返回 nil。
func (s *Server) SelfCheck() error {
	h := handshake.New()
	isn, ack, err := h.RecvSYN("a", 10)
	if err != nil || isn != 0 || ack != 11 {
		return fmt.Errorf("selfcheck: 首次SYN=(%d,%d,%v)", isn, ack, err)
	}
	i2, a2, _ := h.RecvSYN("a", 10) // 不变量3：重复 SYN 幂等
	if i2 != isn || a2 != ack || h.NextISN() != 1 {
		return errors.New("selfcheck: 重复SYN不幂等")
	}
	if _, _, err = h.RecvACK("a", 99); !errors.Is(err, ErrBadAck) { // 不变量4：失败不留痕
		return errors.New("selfcheck: 坏ACK未报ErrBadAck")
	}
	if h.NextISN() != 1 || h.Established("a") {
		return errors.New("selfcheck: 坏ACK改变了状态")
	}
	ci, si, err := h.RecvACK("a", isn+1) // 不变量2：ack==serverISN+1
	if err != nil || ci != 10 || si != isn || !h.Established("a") {
		return errors.New("selfcheck: 握手完成失败")
	}
	if _, _, err = h.RecvACK("a", isn+1); err != nil { // 不变量3：重复 ACK no-op
		return errors.New("selfcheck: 重复ACK不是no-op")
	}
	if _, _, err = h.RecvACK("ghost", 1); !errors.Is(err, ErrHalfOpen) {
		return errors.New("selfcheck: 孤儿ACK未报ErrHalfOpen")
	}
	if _, _, err = h.RecvSYN("b", -1); !errors.Is(err, ErrBadSeq) {
		return errors.New("selfcheck: 负序号未报ErrBadSeq")
	}
	isnB, _, _ := h.RecvSYN("b", 1) // 不变量2：serverISN 单调不重复
	if isnB <= isn {
		return errors.New("selfcheck: serverISN回退或重复")
	}
	return nil
}
