// Package sig 生成目标端签名（块索引 → 弱/强校验和）并支持编解码。
package sig

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"

	"ontology/chunk"
)

// ErrFormat 表示签名编码损坏或不完整，是可判定错误。
var ErrFormat = errors.New("sig: malformed signature")

var magic = []byte("OSS1")

// Entry 是一个完整块的校验和信息。
type Entry struct {
	Weak   uint32
	Strong [32]byte
}

// Signature 描述目标端数据的块校验和索引。Entries[i] 对应第 i 个完整块。
type Signature struct {
	BlockSize int
	DataLen   int
	Entries   []Entry
}

// Generate 为 data 生成签名；只覆盖完整块，末块不满不进签名。
func Generate(data []byte, bs int) (Signature, error) {
	if bs <= 0 {
		return Signature{}, chunk.ErrBlockSize
	}
	n := chunk.FullBlocks(len(data), bs)
	s := Signature{BlockSize: bs, DataLen: len(data), Entries: make([]Entry, 0, n)}
	for i := 0; i < n; i++ {
		block := data[i*bs : (i+1)*bs]
		s.Entries = append(s.Entries, Entry{Weak: chunk.Weak(block), Strong: chunk.Strong(block)})
	}
	return s, nil
}

// Encode 把签名编码为字节流。
func (s Signature) Encode() []byte {
	buf := bytes.NewBuffer(make([]byte, 0, 20+len(s.Entries)*36))
	buf.Write(magic)
	binary.Write(buf, binary.BigEndian, uint32(s.BlockSize))
	binary.Write(buf, binary.BigEndian, uint64(s.DataLen))
	binary.Write(buf, binary.BigEndian, uint32(len(s.Entries)))
	for _, e := range s.Entries {
		binary.Write(buf, binary.BigEndian, e.Weak)
		buf.Write(e.Strong[:])
	}
	return buf.Bytes()
}

// Decode 解码签名；任何截断或格式错误都返回 ErrFormat。
func Decode(raw []byte) (Signature, error) {
	var s Signature
	if len(raw) < 20 || !bytes.Equal(raw[:4], magic) {
		return s, ErrFormat
	}
	s.BlockSize = int(binary.BigEndian.Uint32(raw[4:8]))
	s.DataLen = int(binary.BigEndian.Uint64(raw[8:16]))
	count := int(binary.BigEndian.Uint32(raw[16:20]))
	if s.BlockSize <= 0 {
		return s, fmt.Errorf("%w: bad block size", ErrFormat)
	}
	if len(raw) != 20+count*36 {
		return s, fmt.Errorf("%w: want %d bytes, got %d", ErrFormat, 20+count*36, len(raw))
	}
	raw = raw[20:]
	for i := 0; i < count; i++ {
		var e Entry
		e.Weak = binary.BigEndian.Uint32(raw[:4])
		copy(e.Strong[:], raw[4:36])
		s.Entries = append(s.Entries, e)
		raw = raw[36:]
	}
	return s, nil
}
