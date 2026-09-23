// Package row 定义聚合输入行（分组键 + 数值）及其二进制编解码。
package row

import (
	"encoding/binary"
	"errors"
	"math"
)

// Row 是一条聚合输入：Key 为分组键（允许空串），V 为数值。
type Row struct {
	Key string
	V   float64
}

// ErrBody 表示行体解码失败。
var ErrBody = errors.New("row: bad body")

// Valid 拒绝 NaN（不可比较、不可聚合）；±Inf 与 ±0 均合法。
func (r Row) Valid() bool {
	return !math.IsNaN(r.V)
}

// Encode 把行编码为自描述行体：keylen(uint32 BE) | key | valueBits(uint64 BE)。
func (r Row) Encode() []byte {
	kb := []byte(r.Key)
	buf := make([]byte, 4+len(kb)+8)
	binary.BigEndian.PutUint32(buf, uint32(len(kb)))
	copy(buf[4:], kb)
	binary.BigEndian.PutUint64(buf[4+len(kb):], math.Float64bits(r.V))
	return buf
}

// DecodeBody 解码 Encode 产生的行体。
func DecodeBody(buf []byte) (Row, error) {
	if len(buf) < 4 {
		return Row{}, ErrBody
	}
	kl := int(binary.BigEndian.Uint32(buf))
	if kl < 0 || 4+kl+8 != len(buf) {
		return Row{}, ErrBody
	}
	key := string(buf[4 : 4+kl])
	v := math.Float64frombits(binary.BigEndian.Uint64(buf[4+kl:]))
	return Row{Key: key, V: v}, nil
}
