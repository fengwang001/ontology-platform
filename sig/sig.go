// Package sig 描述目标端签名：每块一对（弱、强）校验和，并提供
// 签名的生成与二进制编解码，供源端 diff 使用。
package sig

import (
	"encoding/binary"
	"errors"
	"fmt"

	"ontology/chunk"
)

// ErrBadSignature 表示签名字节流损坏或格式非法。
var ErrBadSignature = errors.New("sig: malformed signature")

var magic = []byte("ONTS")

const (
	version   = 1
	headerLen = 4 + 1 + 4 + 4 // magic + version + blockSize + numBlocks
	entryLen  = 4 + 16        // weak + strong
)

// BlockSig 是目标端单个块的校验和对。
type BlockSig struct {
	Weak   uint32
	Strong chunk.Strong
}

// Signature 是目标端整份数据的签名。
type Signature struct {
	BlockSize int
	Blocks    []BlockSig
}

// Build 把 data 按 blockSize 切块并生成签名。blockSize 不大于零时报错。
func Build(data []byte, blockSize int) (*Signature, error) {
	n, err := chunk.NumBlocks(len(data), blockSize)
	if err != nil {
		return nil, err
	}
	s := &Signature{BlockSize: blockSize, Blocks: make([]BlockSig, n)}
	for i := range s.Blocks {
		b := chunk.Block(data, blockSize, i)
		s.Blocks[i] = BlockSig{Weak: chunk.SumWeak(b).Value(), Strong: chunk.SumStrong(b)}
	}
	return s, nil
}

// Encode 把签名编码为确定的字节流（大端）。
func (s *Signature) Encode() []byte {
	out := make([]byte, 0, headerLen+len(s.Blocks)*entryLen)
	out = append(out, magic...)
	out = append(out, version)
	out = binary.BigEndian.AppendUint32(out, uint32(s.BlockSize))
	out = binary.BigEndian.AppendUint32(out, uint32(len(s.Blocks)))
	for _, b := range s.Blocks {
		out = binary.BigEndian.AppendUint32(out, b.Weak)
		out = append(out, b.Strong[:]...)
	}
	return out
}

// Decode 解析 Encode 产出的字节流，任何截断或格式错误都报 ErrBadSignature。
func Decode(data []byte) (*Signature, error) {
	if len(data) < headerLen {
		return nil, ErrBadSignature
	}
	if string(data[:4]) != string(magic) || data[4] != version {
		return nil, fmt.Errorf("%w: bad magic or version", ErrBadSignature)
	}
	blockSize := binary.BigEndian.Uint32(data[5:9])
	numBlocks := binary.BigEndian.Uint32(data[9:13])
	if blockSize == 0 {
		return nil, fmt.Errorf("%w: zero block size", ErrBadSignature)
	}
	rest := data[headerLen:]
	if uint64(len(rest)) != uint64(numBlocks)*entryLen {
		return nil, fmt.Errorf("%w: wrong length", ErrBadSignature)
	}
	s := &Signature{BlockSize: int(blockSize), Blocks: make([]BlockSig, numBlocks)}
	for i := range s.Blocks {
		s.Blocks[i].Weak = binary.BigEndian.Uint32(rest[:4])
		copy(s.Blocks[i].Strong[:], rest[4:entryLen])
		rest = rest[entryLen:]
	}
	return s, nil
}
