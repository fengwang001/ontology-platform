// Package gcode 实现标准 Elias gamma 码的单码与流式编解码，仅依赖 bits 包。
package gcode

import (
	"errors"

	"ontology/bits"
)

// 哨兵错误：三类失败互不相同，且可被 errors.Is 判定。
var (
	ErrNonPositive = errors.New("gcode: value must be a positive integer (>=1)") // 含 0 或负数
	ErrTruncated   = errors.New("gcode: truncated code: bits exhausted after leading 1")
	ErrOverflow    = errors.New("gcode: overflow: k>62, value exceeds int64")
)

// EncodeOne 写出正整数 n 的标准 gamma 码：k 个前导 0 位后接 n 的 (k+1) 位二进制。
// n 非正时返回 ErrNonPositive 且不产生任何位。
func EncodeOne(w *bits.Writer, n int64) error {
	if n < 1 {
		return ErrNonPositive
	}
	k := uint(0)
	for v := n; v > 1; v >>= 1 {
		k++
	}
	for i := uint(0); i < k; i++ {
		w.WriteBit(0)
	}
	for b := int(k); b >= 0; b-- {
		w.WriteBit(int((n >> uint(b)) & 1))
	}
	return nil
}

// DecodeOne 从 r 读出一个完整 gamma 码。码起点处全 0 到比特耗尽为合法填充，
// 此时返回 ok=false 且不报错；起始 1 后不足 k 位报 ErrTruncated；k>62 报 ErrOverflow。
func DecodeOne(r *bits.Reader) (n int64, ok bool, err error) {
	r.ResetCheckCount()
	k := 0
	for {
		b, present := r.ReadBit()
		if !present { // 全 0（或码起点即结尾）到耗尽：合法填充
			return 0, false, nil
		}
		if b == 1 {
			break
		}
		k++
	}
	if k > 62 {
		return 0, false, ErrOverflow
	}
	n = int64(1) << uint(k) // 起始 1 贡献 2^k
	for i := 0; i < k; i++ {
		b, present := r.ReadBit()
		if !present {
			return 0, false, ErrTruncated
		}
		n |= int64(b) << uint(k-1-i)
	}
	return n, true, nil
}

// Encode 把正整数序列编码为大端序打包的字节流；空输入返回长度为 0 的 []byte。
// 任一元素非正即整体失败（返回 nil, ErrNonPositive），不产出部分结果。
func Encode(vs []int64) ([]byte, error) {
	w := bits.NewWriter()
	for _, v := range vs {
		if err := EncodeOne(w, v); err != nil {
			return nil, err
		}
	}
	return w.Bytes(), nil
}

// Decode 解码整个字节流。成功时尾部全 0 视为合法填充；
// 失败（ErrTruncated/ErrOverflow）整体失败，返回 nil 切片与错误。
func Decode(p []byte) ([]int64, error) {
	r := bits.NewReader(p)
	out := make([]int64, 0)
	for {
		n, ok, err := DecodeOne(r)
		if err != nil {
			return nil, err
		}
		if !ok {
			return out, nil
		}
		out = append(out, n)
	}
}

// StreamDecoder 增量解码：同一比特流可任意分块经 Feed 喂入，跨块未竟码字
// （已数到的前导 0，或起始 1 后未读全的低位）在 Feed 间保持，故与切分点无关。
type StreamDecoder struct {
	sr    *bits.StreamReader
	phase int // 0=正在数前导 0；1=起始 1 已见、正在读 k 个低位
	k     int // 已数到的前导 0
	val   int64
	rem   int // phase 1 时尚需读的低位个数
}

// NewStreamDecoder 创建空的增量解码器。
func NewStreamDecoder() *StreamDecoder {
	return &StreamDecoder{sr: bits.NewStreamReader()}
}

// Feed 追加一个字节块，返回截至本块能完整解出的整数。块尾未竟的码字被暂存而非报错。
// k>62 在读到起始 1 时即可确定，立即返回 ErrOverflow；出错后的解码器不应继续使用。
func (d *StreamDecoder) Feed(p []byte) ([]int64, error) {
	d.sr.Feed(p)
	var out []int64
	for {
		if d.phase == 0 {
			b, present := d.sr.ReadBit()
			if !present {
				return out, nil // 暂存已数到的 d.k 个前导 0
			}
			if b == 0 {
				d.k++
				continue
			}
			if d.k > 62 {
				return nil, ErrOverflow
			}
			d.val, d.rem, d.phase = int64(1)<<uint(d.k), d.k, 1
		}
		for d.rem > 0 {
			b, present := d.sr.ReadBit()
			if !present {
				return out, nil // 起始 1 已见但低位未读全：暂存待续
			}
			d.rem--
			d.val |= int64(b) << uint(d.rem)
		}
		out = append(out, d.val)
		d.phase, d.k, d.val = 0, 0, 0
	}
}

// End 宣告再无后续数据：尾部全 0（phase 0）为合法填充；
// 若起始 1 已见而低位未读全（phase 1），返回 ErrTruncated。
func (d *StreamDecoder) End() error {
	if d.phase == 1 && d.rem > 0 {
		return ErrTruncated
	}
	return nil
}
