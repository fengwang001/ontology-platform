package ontology

import "sync"

// run 表示一个同值游程：从 start 开始、长度 len 的半开区间 [start, start+len)
// 内所有位都等于 value。游程序列隐含交替取值：偶数下标 value=1，奇数下标
// value=0。端点使用 uint64，最大端点 DomainSize = 2^32，可精确表达长度
// 为 2^32 的全域游程而不发生 uint32 溢出。
type run struct {
	start uint64
	len   uint64
	value uint8
}

// DomainSize 是集合覆盖的位总数：uint32 全域 0..2^32-1，共 2^32 位。
const DomainSize uint64 = 1 << 32

// Bitmap 是基于游程编码（RLE）的 uint32 全域集合，并发安全。
type Bitmap struct {
	mu   sync.RWMutex
	runs []run
}

// New 返回一个空集合。
func New() *Bitmap {
	return &Bitmap{}
}
