// Package norm 提供源/目标列值到目标标度的归一化纯函数。
package norm

import "errors"

// 舍入模式常量。
const (
	RMHalfUp   = 0 // 半入：半数远离零
	RMHalfEven = 1 // 半偶：半数向最近偶数
	RMTruncate = 2 // 截断：朝零
)

// MaxM 为源尾数绝对值上限（|m| <= 10^15），MaxC 为字节串长度上限。
const (
	MaxM int64 = 1_000_000_000_000_000
	MaxC       = 64
)

// ErrParam 表示构造参数或行内取值非法。
var ErrParam = errors.New("norm: invalid parameter")

// N 持有构造参数：目标标度 sd（0..6）、舍入模式 rm、空串等价开关 ne。
type N struct {
	sd int
	rm int
	ne bool
}

// New 构造归一化器；sd 或 rm 越界返回错误。
func New(sd, rm int, ne bool) (*N, error) {
	if sd < 0 || sd > 6 || rm < RMHalfUp || rm > RMTruncate {
		return nil, ErrParam
	}
	return &N{sd: sd, rm: rm, ne: ne}, nil
}

// CvtD 把源尾数 m（标度 6，可空）转换为目标标度尾数。
func (n *N) CvtD(m *int64) *int64 {
	if m == nil {
		return nil
	}
	v := *m
	d := n.scaleDiv()
	if d == 1 {
		r := v
		return &r
	}
	// q 为朝零商：Go 的整数除法本身向零截断。
	q := v / d
	r := v - q*d // r 与 v 同号；v=0 时 r=0
	ar := r
	if ar < 0 {
		ar = -ar
	}
	up := false
	switch n.rm {
	case RMHalfUp:
		up = 2*ar >= d
	case RMHalfEven:
		up = 2*ar > d || (2*ar == d && q&1 != 0)
	case RMTruncate:
		up = false
	}
	if up {
		if r > 0 {
			q++
		} else {
			q--
		}
	}
	return &q
}

// NormC 对字节串去右侧 0x20；nil 保持 NULL，ne 为真时空白串归一为 NULL。
func (n *N) NormC(c []byte) []byte {
	if c == nil {
		return nil
	}
	end := len(c)
	for end > 0 && c[end-1] == 0x20 {
		end--
	}
	if end == 0 {
		if n.ne {
			return nil
		}
		return []byte{}
	}
	out := make([]byte, end)
	copy(out, c[:end])
	return out
}

// SD、RM、NE 回显构造参数，供 diff/plan 包只读使用。
func (n *N) SD() int  { return n.sd }
func (n *N) RM() int  { return n.rm }
func (n *N) NE() bool { return n.ne }

// scaleDiv 返回 D = 10^(6-sd)。
func (n *N) scaleDiv() int64 {
	d := int64(1)
	for i := 0; i < 6-n.sd; i++ {
		d *= 10
	}
	return d
}

// CheckRow 校验行内取值：id 范围、尾数范围、字节串长度；供写入侧复用。
func CheckRow(id int64, d *int64, c []byte) error {
	if id < 1 || id > 1_000_000_000 {
		return ErrParam
	}
	if d != nil && (*d > MaxM || *d < -MaxM) {
		return ErrParam
	}
	if len(c) > MaxC {
		return ErrParam
	}
	return nil
}
