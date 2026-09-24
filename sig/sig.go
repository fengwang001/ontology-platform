// Package sig 定义目标端签名（块索引 → 弱/强校验和）的生成与编解码。
package sig

import (
	"encoding/binary"
	"errors"
	"fmt"

	"ontology/chunk"
)

// ErrZeroBlockSize 表示块大小非正，属可判定错误。
var ErrZeroBlockSize = errors.New("sig: block size must be positive")

const magic = "OSIG"

// Block 是单块签名：弱校验和用于滚动筛选，强校验和用于确认。
type Block struct {
	Weak   uint32
	Strong [32]byte
}

// Signature 是目标端整份数据的签名。
type Signature struct {
	BlockSize int
	TotalLen  int
	Blocks    []Block
}

// Generate 按 size 切分 data 并计算每块的两级校验和；末块不满时按真实长度。
func Generate(data []byte, size int) (Signature, error) {
	if size <= 0 {
		return Signature{}, ErrZeroBlockSize
	}
	n := chunk.NumBlocks(len(data), size)
	s := Signature{BlockSize: size, TotalLen: len(data), Blocks: make([]Block, n)}
	for i := range s.Blocks {
		b := chunk.Block(data, size, i)
		s.Blocks[i] = Block{Weak: chunk.WeakSum(b), Strong: chunk.StrongSum(b)}
	}
	return s, nil
}

// BlockLen 返回第 i 块的长度（末块可能不满）。
func (s Signature) BlockLen(i int) int {
	return chunk.BlockLen(s.TotalLen, s.BlockSize, i)
}

// Encode 序列化签名：magic | blockSize u32 | totalLen u64 | count u32 | 每块 weak u32+strong 32B。
func Encode(s Signature) []byte {
	out := make([]byte, 0, 20+36*len(s.Blocks))
	out = append(out, magic...)
	out = binary.BigEndian.AppendUint32(out, uint32(s.BlockSize))
	out = binary.BigEndian.AppendUint64(out, uint64(s.TotalLen))
	out = binary.BigEndian.AppendUint32(out, uint32(len(s.Blocks)))
	for _, b := range s.Blocks {
		out = binary.BigEndian.AppendUint32(out, b.Weak)
		out = append(out, b.Strong[:]...)
	}
	return out
}

// Decode 反序列化签名，长度不足或格式非法时报错。
func Decode(b []byte) (Signature, error) {
	fail := func() (Signature, error) { return Signature{}, errors.New("sig: malformed encoding") }
	if len(b) < 20 || string(b[:4]) != magic {
		return fail()
	}
	size := int(binary.BigEndian.Uint32(b[4:8]))
	if size <= 0 {
		return Signature{}, ErrZeroBlockSize
	}
	total := int(binary.BigEndian.Uint64(b[8:16]))
	count := int(binary.BigEndian.Uint32(b[16:20]))
	if count < 0 || total < 0 || len(b) != 20+36*count {
		return fail()
	}
	s := Signature{BlockSize: size, TotalLen: total, Blocks: make([]Block, count)}
	off := 20
	for i := range s.Blocks {
		s.Blocks[i].Weak = binary.BigEndian.Uint32(b[off : off+4])
		copy(s.Blocks[i].Strong[:], b[off+4:off+36])
		off += 36
	}
	if got := chunk.NumBlocks(total, size); got != count {
		return Signature{}, fmt.Errorf("sig: block count %d inconsistent with length %d", count, total)
	}
	return s, nil
}
