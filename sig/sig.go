// Package sig 负责目标端块签名的生成与二进制编解码。
package sig

import (
	"encoding/binary"

	"ontology/chunk"
	"ontology/verify"
)

var magic = [4]byte{'O', 'S', 'I', 'G'}

// Entry 是一个块的签名项。
type Entry struct {
	Index  uint32
	Weak   uint32
	Strong [32]byte
}

// Signature 是目标端整份数据的签名。
type Signature struct {
	BlockSize uint32
	Entries   []Entry
}

// Generate 对目标数据切块并生成签名。
func Generate(target []byte, blockSize uint32) (Signature, error) {
	blocks, err := chunk.Split(target, blockSize)
	if err != nil {
		return Signature{}, err
	}
	sig := Signature{BlockSize: blockSize}
	for _, b := range blocks {
		sig.Entries = append(sig.Entries, Entry{
			Index:  uint32(b.Index),
			Weak:   b.Weak,
			Strong: b.Strong,
		})
	}
	return sig, nil
}

// Encode 将签名序列化为小端二进制：
// "OSIG" | blockSize(u32) | {index(u32) weak(u32) strong(32B)}*
func (s Signature) Encode() []byte {
	buf := make([]byte, 0, 8+40*len(s.Entries))
	buf = append(buf, magic[:]...)
	var nb [4]byte
	binary.LittleEndian.PutUint32(nb[:], s.BlockSize)
	buf = append(buf, nb[:]...)
	for _, e := range s.Entries {
		binary.LittleEndian.PutUint32(nb[:], e.Index)
		buf = append(buf, nb[:]...)
		binary.LittleEndian.PutUint32(nb[:], e.Weak)
		buf = append(buf, nb[:]...)
		buf = append(buf, e.Strong[:]...)
	}
	return buf
}

// Decode 解析签名；畸形数据返回可判定错误。
func Decode(b []byte) (Signature, error) {
	if len(b) < 8 {
		return Signature{}, verify.ErrSignature
	}
	if string(b[:4]) != string(magic[:]) {
		return Signature{}, verify.ErrSignature
	}
	n := binary.LittleEndian.Uint32(b[4:8])
	if n == 0 {
		return Signature{}, verify.ErrBadBlockSize
	}
	body := b[8:]
	if len(body)%40 != 0 {
		return Signature{}, verify.ErrSignature
	}
	sig := Signature{BlockSize: n}
	for len(body) > 0 {
		e := Entry{
			Index: binary.LittleEndian.Uint32(body[0:4]),
			Weak:  binary.LittleEndian.Uint32(body[4:8]),
		}
		copy(e.Strong[:], body[8:40])
		sig.Entries = append(sig.Entries, e)
		body = body[40:]
	}
	return sig, nil
}

// Lookup 按弱和建立索引；同一弱和保留全部候选，调用方再用强和确认。
func (s Signature) Lookup() map[uint32][]Entry {
	m := make(map[uint32][]Entry)
	for _, e := range s.Entries {
		m[e.Weak] = append(m[e.Weak], e)
	}
	return m
}
