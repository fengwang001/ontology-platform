// Package limb 提供基 10^9、小端存放的无符号幅值逐 limb 运算。
package limb

// Base 是 limb 的基数，每个 limb 取值 [0, Base)。
const Base = 1_000_000_000

// Mag 是无符号幅值：小端 []uint32，归一化后最高 limb 非零，空切片表示 0。
type Mag []uint32

// Norm 去掉高位零 limb，返回归一化幅值。
func Norm(m Mag) Mag {
	for len(m) > 0 && m[len(m)-1] == 0 {
		m = m[:len(m)-1]
	}
	return m
}

// Cmp 比较两个归一化幅值，返回 -1/0/+1。
func Cmp(a, b Mag) int {
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	for i := len(a) - 1; i >= 0; i-- {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// Add 返回 a+b，带进位传播。
func Add(a, b Mag) Mag {
	if len(a) < len(b) {
		a, b = b, a
	}
	r := make(Mag, len(a)+1)
	var carry uint64
	for i := range a {
		s := uint64(a[i]) + carry
		if i < len(b) {
			s += uint64(b[i])
		}
		r[i] = uint32(s % Base)
		carry = s / Base
	}
	r[len(a)] = uint32(carry)
	return Norm(r)
}

// Sub 返回 a-b，要求 a>=b，带借位传播。
func Sub(a, b Mag) Mag {
	r := make(Mag, len(a))
	var borrow int64
	for i := range a {
		d := int64(a[i]) - borrow
		if i < len(b) {
			d -= int64(b[i])
		}
		if d < 0 {
			d += Base
			borrow = 1
		} else {
			borrow = 0
		}
		r[i] = uint32(d)
	}
	return Norm(r)
}

// MulSmall 返回 a*s，要求 s < Base。
func MulSmall(a Mag, s uint32) Mag {
	r := make(Mag, len(a)+1)
	var carry uint64
	for i, v := range a {
		p := uint64(v)*uint64(s) + carry
		r[i] = uint32(p % Base)
		carry = p / Base
	}
	r[len(a)] = uint32(carry)
	return Norm(r)
}

// Shl 左移 k 个 limb（乘以 Base^k）。
func Shl(a Mag, k int) Mag {
	if len(a) == 0 {
		return nil
	}
	r := make(Mag, len(a)+k)
	copy(r[k:], a)
	return r
}
