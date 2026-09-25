// Package cow 提供固定大小页的引用计数与写时复制原语。
// 它不依赖本模块其他包，也不自带并发保护：调用方（mgmt）负责加锁。
package cow

// Page 是一块固定大小的字节内存，带一个引用计数。
// 引用计数语义：每个存活 view 恰好贡献 1；归零即可释放。
type Page struct {
	data []byte
	refs int
}

// New 创建引用计数为 1 的新页，内容是 data 的独立副本。
func New(data []byte) *Page {
	p := &Page{data: make([]byte, len(data)), refs: 1}
	copy(p.data, data)
	return p
}

// Size 返回页的固定字节大小。
func (p *Page) Size() int { return len(p.data) }

// Refs 返回当前引用计数。
func (p *Page) Refs() int { return p.refs }

// RefsOf 返回 p 的引用计数；p 为 nil（没有任何 view 指向）时返回 0。
func RefsOf(p *Page) int {
	if p == nil {
		return 0
	}
	return p.refs
}

// Incr 把引用计数加 1（新 view 共享本页）。
func (p *Page) Incr() { p.refs++ }

// Decr 把引用计数减 1，返回减后的值；返回 0 表示本页可释放。
func (p *Page) Decr() int {
	p.refs--
	return p.refs
}

// Shared 报告本页是否被多个 view 共享（引用计数 > 1）。
// 写时复制只需要读这“每页一条”的引用记录即可判定，O(1)。
func (p *Page) Shared() bool { return p.refs > 1 }

// Byte 读偏移 off 处的字节（调用方负责边界与共享判定）。
func (p *Page) Byte(off int) byte { return p.data[off] }

// Set 在偏移 off 处原地写字节。仅允许在独占（refs==1）页上调用，
// 或在 Clone 出的新副本上调用。
func (p *Page) Set(off int, val byte) { p.data[off] = val }

// Clone 整页拷贝出一份引用计数为 1 的私有副本，原页内容与计数不变。
func (p *Page) Clone() *Page {
	q := &Page{data: make([]byte, len(p.data)), refs: 1}
	copy(q.data, p.data)
	return q
}

// Content 返回页内容的拷贝，供自检与测试核对。
func (p *Page) Content() []byte {
	out := make([]byte, len(p.data))
	copy(out, p.data)
	return out
}
