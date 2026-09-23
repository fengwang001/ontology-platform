// Package row 定义输入行（分组键 + 数值）及其编解码。
package row

import (
	"encoding/binary"
	"errors"
	"math"
)

// Row 是一条输入记录：分组键 + 数值。
type Row struct {
	Key string
	Val float64
}

// ErrDecode 是行解码失败的哨兵错误。
var ErrDecode = errors.New("row: 行解码失败")

// Encode 把行编码为：uvarint(键长) | 键字节 | 值(uint64 LE 位模式)。
func Encode(r Row) []byte {
	buf := make([]byte, 0, binary.MaxVarintLen64+len(r.Key)+8)
	buf = binary.AppendUvarint(buf, uint64(len(r.Key)))
	buf = append(buf, r.Key...)
	var tmp [8]byte
	binary.LittleEndian.PutUint64(tmp[:], math.Float64bits(r.Val))
	return append(buf, tmp[:]...)
}

// Decode 解码 Encode 的产物，输入长度必须恰好为一条行记录。
func Decode(b []byte) (Row, error) {
	keyLen, n := binary.Uvarint(b)
	if n <= 0 || uint64(len(b)) != uint64(n)+keyLen+8 {
		return Row{}, ErrDecode
	}
	key := string(b[n : uint64(n)+keyLen])
	val := math.Float64frombits(binary.LittleEndian.Uint64(b[uint64(n)+keyLen:]))
	return Row{Key: key, Val: val}, nil
}
