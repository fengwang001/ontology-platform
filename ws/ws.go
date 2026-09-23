// Package ws 做行尾空白（空格与制表符）的延迟判定。
// 只有看到行尾或流结束，才能确定一串空白是否位于行尾。
// 本包不依赖其他包，也不持有实际字节，只维护当前待定空白串的长度与上限。
package ws

import "errors"

// ErrPendingLimit 在待定空白串长度超过配置上限时返回（确定性错误，与切分无关）。
var ErrPendingLimit = errors.New("ws: pending whitespace exceeds limit")

// IsSpace 报告字节是否为行尾空白候选：空格或制表符。
func IsSpace(b byte) bool { return b == ' ' || b == '\t' }

// Decoder 跟踪自上一个“非空白且非行尾”字节以来连续的空白数。
type Decoder struct {
	limit int
	n     int
}

// New 创建上限为 limit（<=0 表示不限）的判定器。
func New(limit int) *Decoder { return &Decoder{limit: limit} }

// AddSpace 记录一个空白字节；超长时返回 ErrPendingLimit，调用方应进入终态。
func (d *Decoder) AddSpace() error {
	d.n++
	if d.limit > 0 && d.n > d.limit {
		return ErrPendingLimit
	}
	return nil
}

// Keep 在出现普通（非空白、非行尾）字节时调用：当前空白串被确认为行中空白，
// 返回其长度并清空计数。
func (d *Decoder) Keep() int {
	n := d.n
	d.n = 0
	return n
}

// Trim 在行尾或流结束时调用：当前空白串被确认为行尾空白，
// 返回其长度（这些字节应删除）并清空计数。
func (d *Decoder) Trim() int {
	n := d.n
	d.n = 0
	return n
}

// Pending 返回当前待定空白串长度。
func (d *Decoder) Pending() int { return d.n }

// Reset 回到初始状态。
func (d *Decoder) Reset() { d.n = 0 }
