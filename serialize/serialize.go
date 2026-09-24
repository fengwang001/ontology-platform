// Package serialize 提供合并结果与冲突报告的落盘读回。
// 格式：magic(4) | version(1) | payloadLen(4) | payload | crc32(payload)(4)。
package serialize

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"math"
	"os"

	"ontology/conflict"
	"ontology/doc"
)

const (
	headerLen = 9 // magic(4) + version(1) + payloadLen(4)
	crcLen    = 4
	version   = 1
)

var magic = []byte("ONT3")

// 三类可判定的截断/损坏错误，均可用 errors.Is 区分。
var (
	ErrHeaderIncomplete = errors.New("serialize: 头部不完整")
	ErrRecordIncomplete = errors.New("serialize: 记录不完整")
	ErrCRCMismatch      = errors.New("serialize: CRC 不匹配")
)

// Marshal 把合并结果与冲突报告编码为自描述字节流。
func Marshal(s doc.Set, rep conflict.Report) []byte {
	payload := doc.Canonical(s)
	payload = appendConflicts(payload, rep.List)
	out := append([]byte{}, magic...)
	out = append(out, version)
	out = binary.BigEndian.AppendUint32(out, uint32(len(payload)))
	out = append(out, payload...)
	out = binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(payload))
	return out
}

// Unmarshal 解码字节流；截断按头部/记录/CRC 三类返回可判定错误。
func Unmarshal(b []byte) (doc.Set, conflict.Report, error) {
	if len(b) < headerLen {
		return nil, conflict.Report{}, ErrHeaderIncomplete
	}
	if string(b[:4]) != string(magic) || b[4] != version {
		return nil, conflict.Report{}, fmt.Errorf("serialize: 魔数或版本不符: %w", ErrHeaderIncomplete)
	}
	payloadLen := int(binary.BigEndian.Uint32(b[5:9]))
	if len(b) < headerLen+payloadLen {
		return nil, conflict.Report{}, ErrRecordIncomplete
	}
	if len(b) < headerLen+payloadLen+crcLen {
		return nil, conflict.Report{}, ErrCRCMismatch
	}
	payload := b[headerLen : headerLen+payloadLen]
	want := binary.BigEndian.Uint32(b[headerLen+payloadLen:])
	if crc32.ChecksumIEEE(payload) != want {
		return nil, conflict.Report{}, ErrCRCMismatch
	}
	s, n, err := doc.Decode(payload)
	if err != nil {
		return nil, conflict.Report{}, fmt.Errorf("%w: %v", ErrRecordIncomplete, err)
	}
	list, err := decodeConflicts(payload[n:])
	if err != nil {
		return nil, conflict.Report{}, fmt.Errorf("%w: %v", ErrRecordIncomplete, err)
	}
	return s, conflict.Report{List: list}, nil
}

// WriteFile 落盘。
func WriteFile(path string, s doc.Set, rep conflict.Report) error {
	return os.WriteFile(path, Marshal(s, rep), 0o644)
}

// ReadFile 读回并校验。
func ReadFile(path string) (doc.Set, conflict.Report, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, conflict.Report{}, err
	}
	return Unmarshal(b)
}

func appendConflicts(buf []byte, list []conflict.Conflict) []byte {
	buf = binary.AppendUvarint(buf, uint64(len(list)))
	for _, c := range list {
		buf = append(buf, byte(c.Kind))
		buf = appendStr(buf, c.Key)
		buf = appendStr(buf, c.Field)
		buf = appendSide(buf, c.Left, c.LeftOK)
		buf = appendSide(buf, c.Right, c.RightOK)
	}
	return buf
}

func appendStr(buf []byte, s string) []byte {
	buf = binary.AppendUvarint(buf, uint64(len(s)))
	return append(buf, s...)
}

func appendSide(buf []byte, v doc.Value, ok bool) []byte {
	if !ok {
		return append(buf, 0)
	}
	buf = append(buf, 1, byte(v.Kind))
	if v.Kind == doc.KindNumber {
		return binary.BigEndian.AppendUint64(buf, math.Float64bits(v.N))
	}
	return appendStr(buf, v.S)
}

type cursor struct {
	b   []byte
	off int
}

func (c *cursor) take(n int) ([]byte, error) {
	if n < 0 || len(c.b)-c.off < n {
		return nil, doc.ErrTruncated
	}
	p := c.b[c.off : c.off+n]
	c.off += n
	return p, nil
}

func (c *cursor) uvarint() (uint64, error) {
	v, n := binary.Uvarint(c.b[c.off:])
	if n <= 0 {
		return 0, doc.ErrTruncated
	}
	c.off += n
	return v, nil
}

func (c *cursor) str() (string, error) {
	n, err := c.uvarint()
	if err != nil {
		return "", err
	}
	p, err := c.take(int(n))
	return string(p), err
}

func (c *cursor) side() (doc.Value, bool, error) {
	p, err := c.take(1)
	if err != nil || p[0] == 0 {
		return doc.Value{}, false, err
	}
	kb, err := c.take(1)
	if err != nil {
		return doc.Value{}, false, err
	}
	if doc.Kind(kb[0]) == doc.KindNumber {
		nb, err := c.take(8)
		if err != nil {
			return doc.Value{}, false, err
		}
		return doc.Num(math.Float64frombits(binary.BigEndian.Uint64(nb))), true, nil
	}
	s, err := c.str()
	return doc.Str(s), true, err
}

func decodeConflicts(b []byte) ([]conflict.Conflict, error) {
	c := &cursor{b: b}
	n, err := c.uvarint()
	if err != nil {
		return nil, err
	}
	list := make([]conflict.Conflict, 0, n)
	for i := uint64(0); i < n; i++ {
		kb, err := c.take(1)
		if err != nil {
			return nil, err
		}
		var cf conflict.Conflict
		cf.Kind = conflict.Kind(kb[0])
		if cf.Key, err = c.str(); err != nil {
			return nil, err
		}
		if cf.Field, err = c.str(); err != nil {
			return nil, err
		}
		if cf.Left, cf.LeftOK, err = c.side(); err != nil {
			return nil, err
		}
		if cf.Right, cf.RightOK, err = c.side(); err != nil {
			return nil, err
		}
		list = append(list, cf)
	}
	return list, nil
}
