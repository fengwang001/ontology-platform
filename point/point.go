// Package point 定义时间序列原始数据点及其线性二进制编解码。
package point

import (
	"encoding/binary"
	"errors"
	"math"
)

// EncodedSize 是单个点编码后的字节数：int64 时间戳 + float64 值。
const EncodedSize = 16

var (
	// ErrShortBuffer 表示缓冲区不足 16 字节，无法解出一个完整点。
	ErrShortBuffer = errors.New("point: buffer shorter than 16 bytes")
	// ErrNaN 表示值为 NaN；NaN 点必须被拒绝并计入跳过数。
	ErrNaN = errors.New("point: NaN value is rejected")
)

// Point 是一个原始观测点：纳秒时间戳（可负）与 float64 值。
type Point struct {
	TS    int64
	Value float64
}

// Valid 校验点值：NaN 非法；±Inf 合法并参与聚合。
func (p Point) Valid() error {
	if math.IsNaN(p.Value) {
		return ErrNaN
	}
	return nil
}

// Encode 将点写入恰好 16 字节的 dst（小端）。
func (p Point) Encode(dst []byte) error {
	if len(dst) < EncodedSize {
		return ErrShortBuffer
	}
	binary.LittleEndian.PutUint64(dst[0:8], uint64(p.TS))
	binary.LittleEndian.PutUint64(dst[8:16], math.Float64bits(p.Value))
	return nil
}

// Decode 从 src 前 16 字节还原一个点；src 不足 16 字节返回 ErrShortBuffer。
func Decode(src []byte) (Point, error) {
	if len(src) < EncodedSize {
		return Point{}, ErrShortBuffer
	}
	return Point{
		TS:    int64(binary.LittleEndian.Uint64(src[0:8])),
		Value: math.Float64frombits(binary.LittleEndian.Uint64(src[8:16])),
	}, nil
}

// EncodeAll 批量编码，返回长度为 16*len(points) 的新切片。
func EncodeAll(points []Point) []byte {
	buf := make([]byte, len(points)*EncodedSize)
	for i := range points {
		_ = points[i].Encode(buf[i*EncodedSize:])
	}
	return buf
}

// DecodeAll 逐 16 字节解码，遇残片停止并返回已解出的点与 ErrShortBuffer。
func DecodeAll(buf []byte) ([]Point, error) {
	n := len(buf) / EncodedSize
	out := make([]Point, 0, n)
	for i := 0; i < n; i++ {
		p, err := Decode(buf[i*EncodedSize:])
		if err != nil {
			return out, err
		}
		out = append(out, p)
	}
	if len(buf)%EncodedSize != 0 {
		return out, ErrShortBuffer
	}
	return out, nil
}
