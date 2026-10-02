package frame

// 游程压缩格式：
//   - 控制字节 c < 128：后接 c+1 个字面字节（1 到 128 个）；
//   - 控制字节 c >= 128：把后一个字节重复 (c-128)+3 次（3 到 130 次）。

const (
	maxLiteral = 128 // 一个字面令牌至多 128 字节
	maxRun     = 130 // 一个重复令牌至多 130 次
)

// compress 按固定算法压缩 src：
// 从左扫描，取当前位置的极大相同字节段长度 r；若 r >= 3，先写出待出字面
// 缓冲（非空时），再写重复令牌，一个令牌至多 130 次，段内余下部分按新的段
// 重新判定；若 r < 3，把这 r 个字节逐个放入字面缓冲，缓冲满 128 个立即写出。
// 结束时写出剩余缓冲。
func compress(src []byte) []byte {
	var out []byte
	var lit []byte
	flushLit := func() {
		if len(lit) > 0 {
			out = append(out, byte(len(lit)-1))
			out = append(out, lit...)
			lit = lit[:0]
		}
	}
	i := 0
	for i < len(src) {
		r := 1
		for i+r < len(src) && src[i+r] == src[i] {
			r++
		}
		if r >= 3 {
			flushLit()
			n := r
			if n > maxRun {
				n = maxRun
			}
			out = append(out, byte(128+(n-3)), src[i])
			i += n
			continue
		}
		for k := 0; k < r; k++ {
			lit = append(lit, src[i])
			i++
			if len(lit) == maxLiteral {
				flushLit()
			}
		}
	}
	flushLit()
	return out
}

// decompress 解压 src，解出超过 maxOut 字节或令牌被截断时返回 ErrDecode。
func decompress(src []byte, maxOut int) ([]byte, error) {
	var out []byte
	i := 0
	for i < len(src) {
		c := src[i]
		i++
		if c < 128 {
			n := int(c) + 1
			if i+n > len(src) {
				return nil, ErrDecode
			}
			out = append(out, src[i:i+n]...)
			i += n
		} else {
			n := int(c) - 128 + 3
			if i >= len(src) {
				return nil, ErrDecode
			}
			b := src[i]
			i++
			for k := 0; k < n; k++ {
				out = append(out, b)
			}
		}
		if len(out) > maxOut {
			return nil, ErrDecode
		}
	}
	return out, nil
}
