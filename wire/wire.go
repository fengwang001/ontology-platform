// Package wire 定义压缩流的字节格式：流头、记录标签与变长整数编码。
package wire

import "encoding/binary"

// Version 是当前流格式版本。
const Version = 1

// Magic 是流头魔数。
var Magic = []byte{'O', 'L', 'Z'}

// 记录标签。
const (
	TagLiteral = 0x00 // 后跟 uvarint 长度与字面量字节
	TagBackref = 0x01 // 后跟 uvarint 距离与 uvarint 长度
	TagFlush   = 0x02 // 刷新标记，无负载
	TagEnd     = 0x03 // 后跟 uvarint 原文总长与 uvarint 校验和
)

// Header 返回流头字节（魔数 + 版本）。
func Header() []byte {
	return append(append([]byte{}, Magic...), Version)
}

// AppendUvarint 把 v 以 uvarint 编码追加到 dst。
func AppendUvarint(dst []byte, v uint64) []byte {
	return binary.AppendUvarint(dst, v)
}

// Checksum 是 FNV-1a 64 位滚动校验和。
type Checksum uint64

// NewChecksum 返回初始化的校验和。
func NewChecksum() Checksum { return 14695981039346656037 }

// Add 把一个字节纳入校验和。
func (c *Checksum) Add(b byte) { *c = (*c ^ Checksum(b)) * 1099511628211 }

// Sum64 返回当前校验和值。
func (c Checksum) Sum64() uint64 { return uint64(c) }
