package sig

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"

	"ontology/chunk"
)

var (
	// ErrBadSignature 表示签名编码不合法。
	ErrBadSignature = errors.New("sig: malformed signature")
)

// Entry 是单个目标块的签名。
type Entry struct {
	Weak   uint16
	Strong []byte // 长度 16
}

// Signature 是目标端全量块签名。
type Signature struct {
	BlockSize int
	Entries   []Entry
}

// Generate 对目标数据生成签名。
func Generate(target []byte, blockSize int) (*Signature, error) {
	blocks, err := chunk.Split(target, blockSize)
	if err != nil {
		return nil, err
	}
	s := &Signature{BlockSize: blockSize, Entries: make([]Entry, len(blocks))}
	for _, blk := range blocks {
		s.Entries[blk.Index] = Entry{Weak: chunk.Weak(blk.Data), Strong: chunk.Strong(blk.Data)}
	}
	return s, nil
}

// Marshal 编码签名：blockSize(4) + 每表项 weak(2)+strong(16)。
func (s *Signature) Marshal() []byte {
	buf := make([]byte, 4+18*len(s.Entries))
	binary.BigEndian.PutUint32(buf, uint32(s.BlockSize))
	off := 4
	for _, e := range s.Entries {
		binary.BigEndian.PutUint16(buf[off:], e.Weak)
		copy(buf[off+2:], e.Strong)
		off += 18
	}
	return buf
}

// Unmarshal 解码签名。
func Unmarshal(buf []byte) (*Signature, error) {
	if len(buf) < 4 || (len(buf)-4)%18 != 0 {
		return nil, fmt.Errorf("%w: bad length %d", ErrBadSignature, len(buf))
	}
	s := &Signature{BlockSize: int(binary.BigEndian.Uint32(buf))}
	if s.BlockSize <= 0 {
		return nil, fmt.Errorf("%w: block size %d", ErrBadSignature, s.BlockSize)
	}
	for off := 4; off < len(buf); off += 18 {
		e := Entry{Weak: binary.BigEndian.Uint16(buf[off:]), Strong: bytes.Clone(buf[off+2 : off+18])}
		s.Entries = append(s.Entries, e)
	}
	return s, nil
}

// Lookup 按弱值返回候选块号。
func (s *Signature) Lookup(weak uint16) []int {
	var ids []int
	for i, e := range s.Entries {
		if e.Weak == weak {
			ids = append(ids, i)
		}
	}
	return ids
}
