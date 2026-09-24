// Package row 定义聚合输入行（分组键 + 数值）及其字节编解码。
package row

import (
	"encoding/binary"
	"errors"
	"math"
)

// Row 是一条聚合输入：Key 为分组键，Val 为参与聚合的数值。
type Row struct {
	Key string
	Val float64
}

var ErrShort = errors.New("row: encoded payload too short")

// Encode 的布局：keylen(uint16 BE) | key | val(float64 BE)。
func (r Row) Encode() []byte {
	b := make([]byte, 2+len(r.Key)+8)
	binary.BigEndian.PutUint16(b, uint16(len(r.Key)))
	copy(b[2:], r.Key)
	binary.BigEndian.PutUint64(b[2+len(r.Key):], math.Float64bits(r.Val))
	return b
}

// Decode 解析 Encode 产生的字节。
func Decode(b []byte) (Row, error) {
	if len(b) < 2 {
		return Row{}, ErrShort
	}
	n := int(binary.BigEndian.Uint16(b))
	if len(b) < 2+n+8 {
		return Row{}, ErrShort
	}
	return Row{
		Key: string(b[2 : 2+n]),
		Val: math.Float64frombits(binary.BigEndian.Uint64(b[2+n:])),
	}, nil
}

// AppendKey 把 keylen(uint16 BE)+key 追加到 buf，供状态序列化复用。
func AppendKey(buf []byte, key string) []byte {
	var lenb [2]byte
	binary.BigEndian.PutUint16(lenb[:], uint16(len(key)))
	buf = append(buf, lenb[:]...)
	return append(buf, key...)
}

// DecodeKey 从 b 头部读出 key，返回 key 与剩余字节。
func DecodeKey(b []byte) (string, []byte, error) {
	if len(b) < 2 {
		return "", nil, ErrShort
	}
	n := int(binary.BigEndian.Uint16(b))
	if len(b) < 2+n {
		return "", nil, ErrShort
	}
	return string(b[2 : 2+n]), b[2+n:], nil
}
