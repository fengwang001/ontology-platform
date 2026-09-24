// Package wire 定义压缩流的字节格式：流头、字面量段、回指、
// 刷新标记与流尾（含原文总长与 FNV-1a-64 校验和），整数一律 uvarint。
// 本包不依赖其他包。
package wire

// 记录标签，以单字节 uvarint 编码。
const (
	TagLiteral = 0 // 字面量段：tag, 长度 n, n 字节原文
	TagBackref = 1 // 回指：tag, 距离 dist, 长度 len
	TagFlush   = 2 // 刷新标记：tag
	TagEnd     = 3 // 流尾：tag, 原文总长, 校验和
)

// MaxVarintLen 是 uvarint 的最大字节数；第 10 字节必须 ≤1，否则 64 位溢出。
const MaxVarintLen = 10

// Version 是当前格式版本。
const Version = 1

// Magic 是流头魔数。
var Magic = []byte{'O', 'L', 'Z', '1'}

// Header 返回完整流头（魔数 + 版本）。
func Header() []byte {
	return append(append([]byte{}, Magic...), Version)
}

// AppendUvarint 把 v 以 uvarint 形式追加到 dst。
func AppendUvarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// FNV-1a-64 参数，用于流尾校验和。
const (
	ChecksumBasis = 14695981039346656037
	checksumPrime = 1099511628211
)

// ChecksumAdd 把一个字节滚动累加进校验和。
func ChecksumAdd(h uint64, b byte) uint64 {
	h ^= uint64(b)
	return h * checksumPrime
}

// Checksum 计算整段字节的 FNV-1a-64 校验和。
func Checksum(p []byte) uint64 {
	h := uint64(ChecksumBasis)
	for _, b := range p {
		h = ChecksumAdd(h, b)
	}
	return h
}
