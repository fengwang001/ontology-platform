// Package ws 延迟判定行尾空白（空格与制表符）。
// 一串空白在行尾出现前必须缓冲：只有看到行尾或流结束才能确定删除还是保留。
package ws

import "errors"

// ErrBufferExceeded 是待判空白串超过配置上限的哨兵错误。
// 拒绝策略：已输出内容保留，调用方应进入终态。错误偏移由调用方（知道绝对位置）附加。
var ErrBufferExceeded = errors.New("ws: trailing whitespace buffer limit exceeded")

// IsSpace 报告 b 是否为行内空白（仅空格与制表符参与行尾裁剪）。
func IsSpace(b byte) bool { return b == ' ' || b == '\t' }

// Buffer 收集一串尚未定性的空白；非空白或行尾出现时由调用方决定 Keep 或 Drop。
type Buffer struct {
	data []byte // 仅含空格/制表符
	lim  int
}

// NewBuffer 创建上限为 limit 字节的空白缓冲；limit<=0 表示不限。
func NewBuffer(limit int) *Buffer {
	return &Buffer{lim: limit}
}

// Len 是当前缓冲的空白字节数。
func (b *Buffer) Len() int { return len(b.data) }

// Add 追加一个空白字节；超过上限返回 ErrBufferExceeded，缓冲保持截断前状态。
func (b *Buffer) Add(c byte) error {
	if b.lim > 0 && len(b.data) >= b.lim {
		return ErrBufferExceeded
	}
	b.data = append(b.data, c)
	return nil
}

// Bytes 返回缓冲的空白串（副本语义：调用方在 Reset 前使用）。
func (b *Buffer) Bytes() []byte { return b.data }

// Reset 清空缓冲（无论 Keep 输出还是 Drop 删除后调用）。
func (b *Buffer) Reset() { b.data = b.data[:0] }
