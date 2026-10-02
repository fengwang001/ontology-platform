// Package ws 实现行尾空白（空格与制表符）的延迟判定缓冲：
// 一串空白只有看到行尾或流结束才能判定是否在行尾，此前必须缓冲。
package ws

import "errors"

// ErrOverflow 表示待定空白串长度超过配置上限（哨兵错误，可用 errors.Is 判定）。
var ErrOverflow = errors.New("ws: pending whitespace exceeds limit")

// Buf 是待定空白缓冲。max < 0 表示不限制。
type Buf struct {
	buf []byte
	max int
}

// New 创建一个容量上限为 max 的缓冲；max < 0 表示不限。
func New(max int) *Buf { return &Buf{max: max} }

// IsWS 报告 b 是否是行尾空白候选字节（空格或制表符）。
func IsWS(b byte) bool { return b == ' ' || b == '\t' }

// Add 追加一个空白字节；超限时返回 ErrOverflow，缓冲保持不变。
func (b *Buf) Add(c byte) error {
	if b.max >= 0 && len(b.buf) >= b.max {
		return ErrOverflow
	}
	b.buf = append(b.buf, c)
	return nil
}

// Len 返回当前待定空白串长度。
func (b *Buf) Len() int { return len(b.buf) }

// Bytes 返回缓冲的空白字节（调用方不得修改）。
func (b *Buf) Bytes() []byte { return b.buf }

// Reset 清空缓冲（在判定完成后调用）。
func (b *Buf) Reset() { b.buf = b.buf[:0] }
