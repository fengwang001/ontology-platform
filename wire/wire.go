// Package wire 定义压缩流的字节格式：流头、字面量段、回指、刷新标记与流尾。
// 所有多字节整数使用 uvarint（LEB128）编码。本包不依赖其他包。
package wire

import "errors"

// 流头：魔数 'O' 'L' + 版本号。
const (
	Magic0  = 'O'
	Magic1  = 'L'
	Version = 1
)

// 记录类型，编码在首 varint 的低 2 位。
const (
	TagLiteral = 0 // tag>>2 为字面量字节数，随后紧跟原始字节
	TagBackref = 1 // 随后为 uvarint dist、uvarint len
	TagFlush   = 2 // 无负载
	TagTrailer = 3 // 随后为 uvarint 原文总长、uvarint 校验和
)

// MaxVarintBytes 是一个 uvarint 允许的最大字节数。
const MaxVarintBytes = 10

var (
	// ErrIncomplete 表示缓冲中的字节不足以解析出完整记录，需要更多输入。
	ErrIncomplete = errors.New("wire: incomplete record")
	// ErrVarintOverflow 表示变长整数超过 10 字节或溢出 64 位。
	ErrVarintOverflow = errors.New("wire: varint overflows 64 bits")
)

// Header 返回流头字节。
func Header() []byte { return []byte{Magic0, Magic1, Version} }

// AppendUvarint 将 v 以 LEB128 追加到 dst。
func AppendUvarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// ReadUvarint 从 b 开头解析 uvarint，返回值与消耗字节数。
// 字节不足返回 ErrIncomplete；超过 10 字节或溢出返回 ErrVarintOverflow。
func ReadUvarint(b []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < MaxVarintBytes; i++ {
		if i >= len(b) {
			return 0, 0, ErrIncomplete
		}
		c := b[i]
		if i == MaxVarintBytes-1 && c > 1 {
			return 0, 0, ErrVarintOverflow
		}
		v |= uint64(c&0x7f) << (7 * i)
		if c < 0x80 {
			return v, i + 1, nil
		}
	}
	return 0, 0, ErrVarintOverflow
}

// AppendLiteral 追加一个字面量段记录。
func AppendLiteral(dst, p []byte) []byte {
	dst = AppendUvarint(dst, uint64(len(p))<<2|TagLiteral)
	return append(dst, p...)
}

// AppendBackref 追加一个回指记录。
func AppendBackref(dst []byte, dist, length uint64) []byte {
	dst = AppendUvarint(dst, TagBackref)
	dst = AppendUvarint(dst, dist)
	return AppendUvarint(dst, length)
}

// AppendFlush 追加一个刷新标记。
func AppendFlush(dst []byte) []byte { return AppendUvarint(dst, TagFlush) }

// AppendTrailer 追加流尾（原文总长 + 校验和）。
func AppendTrailer(dst []byte, total, sum uint64) []byte {
	dst = AppendUvarint(dst, TagTrailer)
	dst = AppendUvarint(dst, total)
	return AppendUvarint(dst, sum)
}

// Checksum 是流格式使用的 FNV-1a 64 校验和。
type Checksum uint64

// SumInit 为 FNV-1a 64 的偏移基。
const SumInit = 14695981039346656037

const sumPrime = 1099511628211

// Add 把一个字节混入校验和。
func (c *Checksum) Add(b byte) { *c = (*c ^ Checksum(b)) * sumPrime }

// AddBytes 把一段字节混入校验和。
func (c *Checksum) AddBytes(p []byte) {
	for _, b := range p {
		c.Add(b)
	}
}
