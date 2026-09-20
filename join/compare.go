package join

import "strings"

// compareCanon 比较两个规范值，给出全序：数值一族按数值，
// 然后 string 按字典序，最后 bool（false 在前）。
func compareCanon(a, b canonVal) int {
	if a.numeric() && b.numeric() {
		return compareNumeric(a, b)
	}
	if rank(a) != rank(b) {
		return rank(a) - rank(b)
	}
	switch a.kind {
	case kindString:
		return strings.Compare(a.s, b.s)
	case kindBool:
		if a.b == b.b {
			return 0
		}
		if !a.b {
			return -1
		}
		return 1
	}
	return 0
}

func rank(c canonVal) int {
	if c.numeric() {
		return 0
	}
	if c.kind == kindString {
		return 1
	}
	return 2
}

// compareNumeric 按数值比较 int64 与 float64 的任意组合。
func compareNumeric(a, b canonVal) int {
	if a.kind == kindInt && b.kind == kindInt {
		switch {
		case a.i < b.i:
			return -1
		case a.i > b.i:
			return 1
		}
		return 0
	}
	if a.kind == kindFloat && b.kind == kindFloat {
		switch {
		case a.f < b.f:
			return -1
		case a.f > b.f:
			return 1
		}
		return 0
	}
	fa, fb := a, b
	neg := false
	if fa.kind == kindInt { // 保证 fa 是 float 一侧，必要时交换
		fa, fb = fb, fa
		neg = true
	}
	r := compareIntFloat(fb.i, fa.f)
	if neg {
		return -r
	}
	return r
}

// compareIntFloat 比较 int64 i 与 float64 f（f 非整数或超出 int64 范围）。
func compareIntFloat(i int64, f float64) int {
	const maxExactInt64 = 9223372036854775808.0
	if f >= maxExactInt64 {
		return -1
	}
	if f < -maxExactInt64 {
		return 1
	}
	fi := int64(f)
	switch {
	case i < fi:
		return -1
	case i > fi:
		return 1
	}
	if f > float64(fi) { // i == trunc(f)，f 还有正的小数部分
		return -1
	}
	if f < float64(fi) {
		return 1
	}
	return 0
}
