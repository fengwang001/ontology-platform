// Package mod 提供不溢出 int64 的模乘与负数归一化，不依赖其他包。
package mod

// Normalize 把任意 int64 归一化到 [0, m)。调用方保证 m > 0。
func Normalize(v, m int64) int64 {
	r := v % m // r ∈ (-m, m)
	if r < 0 {
		r += m // r ∈ (0, m)，不会溢出
	}
	return r
}

// addMod 计算 (x + y) mod m，x、y ∈ [0, m)，m > 0。
// x+y < 2m ≤ 2^64-2，用 uint64 承载不会回绕。
func addMod(x, y, m int64) int64 {
	return int64((uint64(x) + uint64(y)) % uint64(m))
}

// MulMod 计算 (a * b) mod m，不溢出 int64。
// 调用方保证 a、b ∈ [0, m) 且 m > 0；用「加法 + 位移」逐位累加。
func MulMod(a, b, m int64) int64 {
	var r int64
	for b > 0 {
		if b&1 == 1 {
			r = addMod(r, a, m)
		}
		b >>= 1
		if b == 0 {
			break
		}
		a = addMod(a, a, m)
	}
	return r
}
