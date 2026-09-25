// Package cow 实现写时复制页：固定大小字节块 + 引用计数。
// 本包不做并发控制，也不感知 view；并发与映射由上层 mgmt 负责。
package cow

// Page 是固定大小的字节页，带引用计数。
type Page struct {
	data []byte
	ref  int
}

// NewPage 以 data 的拷贝建页，引用计数为 1。
func NewPage(data []byte) *Page {
	d := make([]byte, len(data))
	copy(d, data)
	return &Page{data: d, ref: 1}
}

// Ref 返回当前引用计数。
func (p *Page) Ref() int { return p.ref }

// Inc 引用计数 +1（新增一个共享者）。
func (p *Page) Inc() { p.ref++ }

// Dec 引用计数 -1，返回减后的值；调用方据此判定是否释放。
func (p *Page) Dec() int {
	p.ref--
	return p.ref
}

// Size 返回页大小。
func (p *Page) Size() int { return len(p.data) }

// Read 读 off 处字节。调用方保证 off 合法。
func (p *Page) Read(off int) byte { return p.data[off] }

// Write 原地写 off 处。调用方保证本页独占（Ref()==1）且 off 合法。
func (p *Page) Write(off int, val byte) { p.data[off] = val }

// Clone 整页拷贝出一个新页，引用计数为 1；本页不受影响。
func (p *Page) Clone() *Page {
	return &Page{data: append([]byte(nil), p.data...), ref: 1}
}

// Bytes 返回内容拷贝（供自检对比）。
func (p *Page) Bytes() []byte { return append([]byte(nil), p.data...) }
