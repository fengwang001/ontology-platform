// Package sig 负责目标端签名（块索引 → 两级校验和）的生成与编解码。
package sig

import (
	"bytes"
	"encoding/binary"
	"errors"

	"ontology/chunk"
)

// ErrBadSignature 表示签名字节流残缺或格式非法。
var ErrBadSignature = errors.New("sig: malformed signature")

// Signature 是目标端各块校验和的索引，块号即数组下标。
type Signature struct {
	BlockSize int
	Target    [16]byte // 目标数据的整体强校验和
	Weak      []uint32
	Strong    [][16]byte
}

// Build 对目标数据分块并生成签名。
func Build(data []byte, blockSize int) (Signature, error) {
	blocks, err := chunk.Split(data, blockSize)
	if err != nil {
		return Signature{}, err
	}
	s := Signature{BlockSize: blockSize, Target: chunk.Strong(data)}
	for _, b := range blocks {
		s.Weak = append(s.Weak, b.Weak)
		s.Strong = append(s.Strong, b.Strong)
	}
	return s, nil
}

// NumBlocks 返回签名覆盖的块数。
func (s Signature) NumBlocks() int { return len(s.Weak) }

var magic = []byte("ONS1")

const headerLen = 4 + 4 + 4 + 16 // magic | blockSize | count | targetHash
const entryLen = 4 + 16          // weak | strong

// Encode 把签名编码为字节流。
func (s Signature) Encode() []byte {
	out := make([]byte, 0, headerLen+len(s.Weak)*entryLen)
	out = append(out, magic...)
	out = appendU32(out, uint32(s.BlockSize))
	out = appendU32(out, uint32(len(s.Weak)))
	out = append(out, s.Target[:]...)
	for i := range s.Weak {
		out = appendU32(out, s.Weak[i])
		out = append(out, s.Strong[i][:]...)
	}
	return out
}

// Decode 解析签名字节流，长度不符或 magic 错误时报 ErrBadSignature。
func Decode(b []byte) (Signature, error) {
	if len(b) < headerLen || !bytes.Equal(b[:4], magic) {
		return Signature{}, ErrBadSignature
	}
	n := int(binary.BigEndian.Uint32(b[8:12]))
	if len(b) != headerLen+n*entryLen {
		return Signature{}, ErrBadSignature
	}
	s := Signature{BlockSize: int(binary.BigEndian.Uint32(b[4:8]))}
	copy(s.Target[:], b[12:headerLen])
	off := headerLen
	for i := 0; i < n; i++ {
		s.Weak = append(s.Weak, binary.BigEndian.Uint32(b[off:off+4]))
		var st [16]byte
		copy(st[:], b[off+4:off+entryLen])
		s.Strong = append(s.Strong, st)
		off += entryLen
	}
	return s, nil
}

func appendU32(b []byte, v uint32) []byte {
	var t [4]byte
	binary.BigEndian.PutUint32(t[:], v)
	return append(b, t[:]...)
}
