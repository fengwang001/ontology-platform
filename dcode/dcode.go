// Package dcode 实现 Elias delta 码：gamma 辅助、单码编解码与流级编解码。
// 依赖 bits 包，不依赖 api。
package dcode

import (
	"errors"
	"io"

	"ontology/bits"
)

// 三个互不相同的哨兵错误，可用 errors.Is 判定。
var (
	ErrNonPositive = errors.New("dcode: 元素不是正整数")
	ErrTruncated   = errors.New("dcode: 比特流在码中途耗尽")
	ErrOverflow    = errors.New("dcode: 码长 L 超过 63，值超出 int64")
)

// clz64 返回 m 的前导零个数；m 必须非零。
func clz64(m uint64) uint {
	n := uint(0)
	for i := 63; i >= 0; i-- {
		if m>>uint(i)&1 == 1 {
			return n
		}
		n++
	}
	return n
}

// writeGamma 写出 gamma(m) = (k-1) 个 0 + binary(m)，k 为 m 的二进制位数。
func writeGamma(w *bits.Writer, m uint64) {
	k := uint(64 - clz64(m))
	for i := uint(1); i < k; i++ {
		w.WriteBit(0)
	}
	w.WriteBits(m, k)
}

// EncodeOne 把单个正整数 n 的 delta 码写入 w；n 非正时返回 ErrNonPositive。
func EncodeOne(w *bits.Writer, n int64) error {
	if n < 1 {
		return ErrNonPositive
	}
	L := uint(64 - clz64(uint64(n)))
	writeGamma(w, uint64(L))
	w.WriteBits(uint64(n), L-1) // 值层：binary(n) 去掉最高位 1 后的 L-1 位
	return nil
}

// readGamma 从 r 解出一个 gamma 码的值（>= 1）；读到 1 前比特耗尽属截断。
func readGamma(r *bits.Reader) (uint64, error) {
	zeros := uint(0)
	for {
		b, err := r.ReadBit()
		if err != nil {
			return 0, err
		}
		if b == 1 {
			break
		}
		zeros++
		if zeros >= 64 { // 64 个及以上前导 0：gamma 值必 >= 2^64，无法表示
			return 0, ErrOverflow
		}
	}
	rest, err := r.ReadBits(zeros)
	if err != nil {
		return 0, err
	}
	return 1<<zeros | rest, nil
}

// DecodeOne 从 r 解出一个 delta 码；中途比特耗尽返回 ErrTruncated，
// L > 63 返回 ErrOverflow。起点处对 bits.Reader 的非导出计数器打点，
// 检查位数随后由读取过程自动维护。
func DecodeOne(r *bits.Reader) (int64, error) {
	n, _, err := decodeOneCount(r)
	return n, err
}

// decodeOneCount 与 DecodeOne 相同，额外返回本次为读出完整码而检查的位数。
// 非导出：仅供包内测试断言「不重扫前缀」，不出现在任何公开接口。
func decodeOneCount(r *bits.Reader) (n int64, checked uint64, err error) {
	start := r.Pos()
	r.StartCode()
	L, err := readGamma(r)
	if err != nil {
		if errors.Is(err, io.EOF) {
			err = ErrTruncated
		}
		return 0, 0, err
	}
	if L > 63 {
		return 0, 0, ErrOverflow
	}
	low, err := r.ReadBits(uint(L - 1))
	if err != nil {
		return 0, 0, ErrTruncated
	}
	return int64(1<<(L-1) | low), r.Pos() - start, nil
}

// Encode 把正整数序列编码为字节流；任一元素非正返回 ErrNonPositive。
func Encode(vs []int64) ([]byte, error) {
	w := bits.NewWriter()
	for _, v := range vs {
		if err := EncodeOne(w, v); err != nil {
			return nil, err
		}
	}
	return w.Bytes(), nil
}

// Decode 整体解码字节流；出错时返回 nil 与哨兵错误，不返回部分前缀。
func Decode(data []byte) ([]int64, error) {
	r := bits.NewReader(data)
	var out []int64
	for !r.TailIsZeroPadding() {
		n, err := DecodeOne(r)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

// StreamDecoder 增量解码器：任意切块 Feed，Decode 结果与一次性 Decode 一致。
type StreamDecoder struct {
	buf []byte
}

// NewStreamDecoder 创建增量解码器。
func NewStreamDecoder() *StreamDecoder { return &StreamDecoder{} }

// Feed 追加一段字节（可以是任意切分的一块）。
func (d *StreamDecoder) Feed(p []byte) { d.buf = append(d.buf, p...) }

// Decode 解码目前已喂入的全部字节；失败返回 nil，内部缓冲保持不变，可继续使用。
func (d *StreamDecoder) Decode() ([]int64, error) { return Decode(d.buf) }
