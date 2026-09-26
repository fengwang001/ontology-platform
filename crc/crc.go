// Package crc 实现 CRC-32 的 256 项查表与逐位参照两种算法。
package crc

// 规则给定的常量：初始值与最终异或。
const (
	Init   uint32 = 0xFFFFFFFF
	XorOut uint32 = 0xFFFFFFFF
)

// CRC 持有某一反射多项式的 256 项表与累计状态。
type CRC struct {
	poly  uint32
	table [256]uint32
	crc   uint32
	// bitLoops 记录最近一次更新走过的逐位循环次数（表驱动恒为 0）。
	// 非导出，不出现在任何公开接口。
	bitLoops int
}

// New 用逐位循环从初值 0 对字节 i 跑 8 次，生成 table[i]。
func New(poly uint32) *CRC {
	c := &CRC{poly: poly, crc: Init}
	for i := 0; i < 256; i++ {
		v := uint32(i)
		for k := 0; k < 8; k++ {
			if v&1 != 0 {
				v = poly ^ (v >> 1)
			} else {
				v >>= 1
			}
		}
		c.table[i] = v
	}
	return c
}

// UpdateTable 表驱动更新：crc = table[(crc^b)&0xFF] ^ (crc>>8)。
func (c *CRC) UpdateTable(p []byte) {
	c.bitLoops = 0
	for _, b := range p {
		c.crc = c.table[(c.crc^uint32(b))&0xFF] ^ (c.crc >> 8)
	}
}

// UpdateBitwise 逐位参照更新：每字节先 crc ^= b，再做 8 次逐位循环。
func (c *CRC) UpdateBitwise(p []byte) {
	c.bitLoops = 8 * len(p)
	for _, b := range p {
		c.crc ^= uint32(b)
		for k := 0; k < 8; k++ {
			if c.crc&1 != 0 {
				c.crc = c.poly ^ (c.crc >> 1)
			} else {
				c.crc >>= 1
			}
		}
	}
}

// Value 返回当前校验和：crc ^ XorOut。
func (c *CRC) Value() uint32 {
	return c.crc ^ XorOut
}
