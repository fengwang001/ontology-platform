// Package ws 延迟判定空格与制表符：只有看到行尾或流结束，
// 才能知道一串连续空白是否位于行尾。它只管缓冲，不做行尾识别。
package ws

import "errors"

// ErrLimit 表示待定空白累计长度超过配置上限。
var ErrLimit = errors.New("ws: pending whitespace limit exceeded")

// IsSpace 报告 b 是否为参与判定的空白（空格或制表符）。
func IsSpace(b byte) bool { return b == ' ' || b == '\t' }

// Pending 保存一串尚未判定的空白及其原文起点。
type Pending struct {
	// Data 是待定空白字节。
	Data []byte
	// Start 是 Data[0] 在当前输入流中的原文偏移。
	Start int
}

// Active 报告是否持有待定空白。
func (p *Pending) Active() bool { return len(p.Data) > 0 }

// Len 返回待定空白长度。
func (p *Pending) Len() int { return len(p.Data) }

// Clear 丢弃整串（判定为行尾空白）。
func (p *Pending) Clear() {
	p.Data = p.Data[:0]
	p.Start = 0
}

// Take 返回整串（含其原文起点）并清空（判定为行内空白，需要原样输出）。
func (p *Pending) Take() ([]byte, int) {
	b, s := p.Data, p.Start
	p.Data = nil
	p.Start = 0
	return b, s
}

// Peek 返回待定空白字节的副本（不清空），供片段模式携带尾部。
func (p *Pending) Peek() []byte {
	out := make([]byte, len(p.Data))
	copy(out, p.Data)
	return out
}

// Begin 开启一串新的待定空白；已有内容时忽略（调用方应先裁决旧串）。
func (p *Pending) Begin(off int) {
	if p.Active() {
		return
	}
	p.Data = p.Data[:0]
	p.Start = off
}

// Append 追加一个空白字节，累计长度超过 limit 时返回 ErrLimit 且不改变状态。
// limit <= 0 表示不限制。
func (p *Pending) Append(b byte, limit int) error {
	if limit > 0 && len(p.Data) >= limit {
		return ErrLimit
	}
	p.Data = append(p.Data, b)
	return nil
}

// AppendBytes 批量追加，超限行为同 Append：超限字节不接收，返回 ErrLimit。
func (p *Pending) AppendBytes(b []byte, limit int) error {
	for _, c := range b {
		if err := p.Append(c, limit); err != nil {
			return err
		}
	}
	return nil
}
