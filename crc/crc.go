// Package crc 提供 256 项查表与逐位参照两种 CRC-32 更新实现。依赖 poly。
package crc

// Table 是某个反射多项式对应的 256 项查表。
type Table struct {
	t    [256]uint32
	poly uint32
}

// NewTable 为反射多项式 p 生成 256 项表：table[i] = 从初值 0 出发
// 对字节 i 执行 8 次逐位循环后的值。调用方须保证 p 合法（见 poly.Valid）。
func NewTable(p uint32) *Table {
	tab := &Table{poly: p}
	for i := 0; i < 256; i++ {
		c := uint32(i)
		for k := 0; k < 8; k++ {
			if c&1 != 0 {
				c = p ^ (c >> 1)
			} else {
				c >>= 1
			}
		}
		tab.t[i] = c
	}
	return tab
}

// Digest 是累计中的 CRC 状态。
type Digest struct {
	tab *Table
	crc uint32
	// lastBitwise 记录最近一次更新走过的逐位循环次数：
	// 表驱动更新恒为 0，逐位参照更新为 8*len(p)。非导出，不进公开接口。
	lastBitwise int
}

// NewDigest 以 tab 与初始值 init 新建一个 Digest。
func NewDigest(tab *Table, init uint32) *Digest {
	return &Digest{tab: tab, crc: init}
}

// UpdateTable 表驱动更新：每字节一次查表，逐位循环次数为 0。
func (d *Digest) UpdateTable(p []byte) {
	for _, b := range p {
		d.crc = d.tab.t[(d.crc^uint32(b))&0xFF] ^ (d.crc >> 8)
	}
	d.lastBitwise = 0
}

// UpdateBitwise 逐位参照更新：每字节 8 次逐位循环，仅作正确性基准。
func (d *Digest) UpdateBitwise(p []byte) {
	for _, b := range p {
		d.crc ^= uint32(b)
		for k := 0; k < 8; k++ {
			if d.crc&1 != 0 {
				d.crc = d.tab.poly ^ (d.crc >> 1)
			} else {
				d.crc >>= 1
			}
		}
	}
	d.lastBitwise = 8 * len(p)
}

// Raw 返回当前未异或 xorout 的内部 crc。
func (d *Digest) Raw() uint32 { return d.crc }

// TableDriven 报告最近一次更新是否走了表驱动路径（逐位循环次数为 0）。
// 只暴露一个布尔判定，不暴露计数器数值本身。
func (d *Digest) TableDriven() bool { return d.lastBitwise == 0 }
