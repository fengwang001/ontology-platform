// Package row 定义聚合输入行及其自描述字节编解码。
package row

import (
	"encoding/binary"
	"errors"
	"math"
)

// Row 是一条聚合输入：分组键 Key 与数值 V。
type Row struct {
	Key string
	V   float64
}

// 载荷布局：keylen(2 大端) | key | value(8)。
const valueSize = 8

var ErrShort = errors.New("row: payload too short")
var ErrKeyTooLong = errors.New("row: key longer than 65535")

// IsNaN 报告数值是否为 NaN（NaN 在入表前被拒绝）。
func IsNaN(v float64) bool { return v != v }

// Encode 把行编码为追加到 dst 的字节片。
func Encode(dst []byte, r Row) ([]byte, error) {
	if len(r.Key) > math.MaxUint16 {
		return nil, ErrKeyTooLong
	}
	var lenBuf [2]byte
	binary.BigEndian.PutUint16(lenBuf[:], uint16(len(r.Key)))
	dst = append(dst, lenBuf[:]...)
	dst = append(dst, r.Key...)
	var vBuf [valueSize]byte
	binary.BigEndian.PutUint64(vBuf[:], math.Float64bits(r.V))
	return append(dst, vBuf[:]...), nil
}

// Decode 从载荷首条记录解码出行，并返回剩余字节。
func Decode(buf []byte) (Row, []byte, error) {
	if len(buf) < 2 {
		return Row{}, nil, ErrShort
	}
	keyLen := int(binary.BigEndian.Uint16(buf[:2]))
	if len(buf) < 2+keyLen+valueSize {
		return Row{}, nil, ErrShort
	}
	key := string(buf[2 : 2+keyLen])
	bits := binary.BigEndian.Uint64(buf[2+keyLen : 2+keyLen+valueSize])
	rest := buf[2+keyLen+valueSize:]
	return Row{Key: key, V: math.Float64frombits(bits)}, rest, nil
}

// EncodedLen 返回行编码后的字节数。
func EncodedLen(r Row) int {
	return 2 + len(r.Key) + valueSize
}
