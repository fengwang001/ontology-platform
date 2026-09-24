// Package serialize 负责合并结果与冲突报告的落盘读回。
// 格式：magic(4) | payloadLen(uint32 LE) | payload | crc32(payload, 4 LE)。
package serialize

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"math"
	"os"

	"ontology/conflict"
	"ontology/doc"
)

var (
	ErrHeaderIncomplete = errors.New("serialize: header incomplete") // 头部不完整
	ErrRecordIncomplete = errors.New("serialize: record incomplete") // payload 不完整
	ErrCRCMismatch      = errors.New("serialize: crc mismatch")      // CRC 缺失或不匹配
)

var magic = []byte{'O', 'M', '3', 1}

const headerLen = 8 // magic(4) + payloadLen(4)

// Encode 把合并结果与冲突清单编码为自描述字节流（确定性）。
func Encode(merged doc.Set, conflicts []conflict.Conflict) []byte {
	payload := doc.Encode(merged)
	payload = appendConflicts(payload, conflicts)
	var hdr [headerLen]byte
	copy(hdr[:4], magic)
	binary.LittleEndian.PutUint32(hdr[4:], uint32(len(payload)))
	out := append([]byte{}, hdr[:]...)
	out = append(out, payload...)
	var crc [4]byte
	binary.LittleEndian.PutUint32(crc[:], crc32.ChecksumIEEE(payload))
	return append(out, crc[:]...)
}

// Decode 校验并解码字节流；三类截断/损坏分别返回可 errors.Is 判定的错误。
func Decode(b []byte) (doc.Set, []conflict.Conflict, error) {
	if len(b) < headerLen || string(b[:4]) != string(magic) {
		return nil, nil, ErrHeaderIncomplete
	}
	plen := int(binary.LittleEndian.Uint32(b[4:8]))
	if len(b) < headerLen+plen {
		return nil, nil, ErrRecordIncomplete
	}
	payload := b[headerLen : headerLen+plen]
	if len(b) < headerLen+plen+4 {
		return nil, nil, ErrCRCMismatch
	}
	want := binary.LittleEndian.Uint32(b[headerLen+plen:])
	if crc32.ChecksumIEEE(payload) != want {
		return nil, nil, ErrCRCMismatch
	}
	merged, rest, err := decodeSet(payload)
	if err != nil {
		return nil, nil, err
	}
	cs, err := decodeConflicts(rest)
	return merged, cs, err
}

// WriteFile 落盘。
func WriteFile(path string, merged doc.Set, conflicts []conflict.Conflict) error {
	return os.WriteFile(path, Encode(merged, conflicts), 0o644)
}

// ReadFile 读回并校验。
func ReadFile(path string) (doc.Set, []conflict.Conflict, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	return Decode(b)
}

func appendU32(b []byte, n uint32) []byte {
	var v [4]byte
	binary.LittleEndian.PutUint32(v[:], n)
	return append(b, v[:]...)
}

func appendStr(b []byte, s string) []byte {
	return append(appendU32(b, uint32(len(s))), s...)
}

func appendConflicts(b []byte, cs []conflict.Conflict) []byte {
	b = appendU32(b, uint32(len(cs)))
	for _, c := range conflict.Sorted(cs) {
		b = append(b, byte(c.Kind))
		b = appendStr(b, c.Key)
		b = appendStr(b, c.Field)
		b = appendStr(b, c.LeftType)
		b = appendStr(b, c.RightType)
	}
	return b
}

type reader struct {
	b   []byte
	off int
	err bool
}

func (r *reader) u32() uint32 {
	if r.off+4 > len(r.b) {
		r.err = true
		return 0
	}
	v := binary.LittleEndian.Uint32(r.b[r.off:])
	r.off += 4
	return v
}

func (r *reader) str() string {
	n := int(r.u32())
	if r.err || r.off+n > len(r.b) {
		r.err = true
		return ""
	}
	s := string(r.b[r.off : r.off+n])
	r.off += n
	return s
}

func (r *reader) value() any {
	if r.off >= len(r.b) {
		r.err = true
		return nil
	}
	tag := r.b[r.off]
	r.off++
	switch tag {
	case 'n':
		return nil
	case 't', 'f':
		return tag == 't'
	case 's':
		return r.str()
	case 'i', 'I', 'F':
		if r.off+8 > len(r.b) {
			r.err = true
			return nil
		}
		bits := binary.LittleEndian.Uint64(r.b[r.off:])
		r.off += 8
		switch tag {
		case 'i':
			return int(int64(bits))
		case 'I':
			return int64(bits)
		}
		return math.Float64frombits(bits)
	}
	r.err = true
	return nil
}

func decodeSet(b []byte) (doc.Set, []byte, error) {
	r := &reader{b: b}
	n := r.u32()
	s := make(doc.Set, n)
	for i := uint32(0); i < n && !r.err; i++ {
		k := r.str()
		nf := r.u32()
		rec := make(doc.Record, nf)
		for j := uint32(0); j < nf && !r.err; j++ {
			rec[r.str()] = r.value()
		}
		s[k] = rec
	}
	if r.err {
		return nil, nil, ErrRecordIncomplete
	}
	return s, r.b[r.off:], nil
}

func decodeConflicts(b []byte) ([]conflict.Conflict, error) {
	r := &reader{b: b}
	n := r.u32()
	cs := make([]conflict.Conflict, 0, n)
	for i := uint32(0); i < n && !r.err; i++ {
		if r.off >= len(r.b) {
			return nil, ErrRecordIncomplete
		}
		c := conflict.Conflict{Kind: conflict.Kind(r.b[r.off])}
		r.off++
		c.Key = r.str()
		c.Field = r.str()
		c.LeftType = r.str()
		c.RightType = r.str()
		cs = append(cs, c)
	}
	if r.err {
		return nil, ErrRecordIncomplete
	}
	return cs, nil
}
