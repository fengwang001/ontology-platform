// Package ws 实现行尾空白（空格与制表符）的延迟判定缓冲：
// 一串空白只有看到行尾或流结束才知道是否在行尾，因此必须先缓冲，
// 不能提前输出，也不能提前丢弃。不依赖其他包。
package ws

// Pend 保存一串尚未判定的空白字节及其在原文中的起始偏移。
// Limit 为缓冲上限（字节数），0 表示不限。
type Pend struct {
	Limit int
	buf   []byte
	start int
}

// Add 追加一个位于原文偏移 pos 的空白字节。
// 返回 false 表示超出 Limit（该字节未被接收），调用方应报错。
func (p *Pend) Add(b byte, pos int) bool {
	if len(p.buf) == 0 {
		p.start = pos
	}
	if p.Limit > 0 && len(p.buf) >= p.Limit {
		return false
	}
	p.buf = append(p.buf, b)
	return true
}

// Len 返回已缓冲字节数。
func (p *Pend) Len() int { return len(p.buf) }

// Start 返回缓冲串在原文中的起始偏移。
func (p *Pend) Start() int { return p.start }

// Bytes 返回缓冲的空白字节（不得修改）。
func (p *Pend) Bytes() []byte { return p.buf }

// Reset 清空缓冲（判定完成后调用）。
func (p *Pend) Reset() { p.buf = p.buf[:0] }
