package ontology

// Vector 是版本向量，按分量顺序做字典序比较。
type Vector []int64

// Clone 返回向量的深拷贝。
func (v Vector) Clone() Vector {
	c := make(Vector, len(v))
	copy(c, v)
	return c
}

// Compare 按分量顺序做字典序比较：a 大返回 1，b 大返回 -1，相等返回 0。
// 长度不同的向量不可比，调用方需保证长度一致。
func Compare(a, b Vector) int {
	for i := range a {
		switch {
		case a[i] > b[i]:
			return 1
		case a[i] < b[i]:
			return -1
		}
	}
	return 0
}

// LessEqual 判断 a 是否逐分量不超过 b。
func LessEqual(a, b Vector) bool {
	for i := range a {
		if a[i] > b[i] {
			return false
		}
	}
	return true
}

// Max 返回两向量逐分量取最大值的新向量。
func Max(a, b Vector) Vector {
	m := make(Vector, len(a))
	for i := range a {
		if a[i] >= b[i] {
			m[i] = a[i]
		} else {
			m[i] = b[i]
		}
	}
	return m
}
