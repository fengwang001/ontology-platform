// Package norm 提供异构列值到目标标度与补位规则的归一化。
package norm

import "errors"

// 舍入模式。
const (
	RmHalfAway = 0 // 半数远离零
	RmHalfEven = 1 // 半数取偶
	RmTruncate = 2 // 朝零截断
)

// 取值边界。
const (
	MaxID    = 1_000_000_000
	MaxMant  = 1_000_000_000_000_000 // |m| <= 10^15
	MaxBytes = 64
)

var (
	// ErrInvalid 表示参数或行内取值越界。
	ErrInvalid = errors.New("norm: invalid argument")
)

// Row 是一行 (id, d, c)。指针字段为 NULL。
type Row struct {
	ID int64
	D  *int64
	C  []byte
}

// Cfg 是归一化配置：sd 目标标度(0..6)，Rm 舍入模式，NE 空串视同 NULL。
type Cfg struct {
	sd int
	rm int
	ne bool
}

// NewCfg 构造并校验配置。
func NewCfg(sd, rm int, ne bool) (Cfg, error) {
	if sd < 0 || sd > 6 || rm < RmHalfAway || rm > RmTruncate {
		return Cfg{}, ErrInvalid
	}
	return Cfg{sd: sd, rm: rm, ne: ne}, nil
}

// CheckRow 校验 id、d、c 的取值范围。
func CheckRow(r Row) error {
	if r.ID < 1 || r.ID > MaxID {
		return ErrInvalid
	}
	if r.D != nil {
		m := *r.D
		if m < -MaxMant || m > MaxMant {
			return ErrInvalid
		}
	}
	if r.C != nil && len(r.C) > MaxBytes {
		return ErrInvalid
	}
	return nil
}

// CvtD 把源标度 6 的尾数转换为目标标度尾数。
func (c Cfg) CvtD(m int64) int64 {
	if c.sd == 6 {
		return m
	}
	div := int64(pow10(6 - c.sd))
	q := m / div
	r := m - q*div
	if r == 0 || c.rm == RmTruncate {
		return q
	}
	sign := int64(1)
	if m < 0 {
		sign = -1
	}
	ar := r
	if ar < 0 {
		ar = -ar
	}
	two := 2 * ar // 2|r| <= 2*10^15，int64 安全
	switch c.rm {
	case RmHalfAway:
		if two >= div {
			q += sign
		}
	case RmHalfEven:
		if two > div || (two == div && q&1 != 0) {
			q += sign
		}
	}
	return q
}

// CvtDNil 保留 NULL。
func (c Cfg) CvtDNil(m *int64) *int64 {
	if m == nil {
		return nil
	}
	v := c.CvtD(*m)
	return &v
}

// NormC 去掉右侧 0x20，ne 真时空串归一为 NULL。
func (c Cfg) NormC(b []byte) []byte {
	if b == nil {
		return nil
	}
	n := len(b)
	for n > 0 && b[n-1] == 0x20 {
		n--
	}
	out := make([]byte, n)
	copy(out, b[:n])
	if n == 0 && c.ne {
		return nil
	}
	return out
}

// CvtRow 返回 d、c 都按源端规则归一化后的行副本。
func (c Cfg) CvtRow(r Row) Row {
	return Row{ID: r.ID, D: c.CvtDNil(r.D), C: c.NormC(r.C)}
}

// TgtRow 返回目标端归一化后的行副本（d 原样，c 去空格）。
func (c Cfg) TgtRow(r Row) Row {
	var d *int64
	if r.D != nil {
		v := *r.D
		d = &v
	}
	return Row{ID: r.ID, D: d, C: c.NormC(r.C)}
}

// SD 返回目标标度。
func (c Cfg) SD() int { return c.sd }

// Rm 返回舍入模式。
func (c Cfg) Rm() int { return c.rm }

// NE 返回空串是否视同 NULL。
func (c Cfg) NE() bool { return c.ne }

func pow10(n int) int {
	v := 1
	for i := 0; i < n; i++ {
		v *= 10
	}
	return v
}
