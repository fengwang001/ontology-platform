// Package hashk 计算布隆过滤器的 k 个哈希位置，不依赖其他包。
package hashk

// H1 返回 (Σ byte_i) mod m。
func H1(x []byte, m int) int {
	var sum int64
	for _, b := range x {
		sum += int64(b)
	}
	return int(sum % int64(m))
}

// H2 返回 1 + ((Σ byte_i·(i+1)) mod (m-1))。
// m==1 时按题面公式模数为 0，此时位数组只有 1 位，H2 恒取 1（pos 仍恒为 0）。
func H2(x []byte, m int) int {
	mod := int64(m - 1)
	if mod == 0 {
		return 1
	}
	var sum int64
	for i, b := range x {
		sum += int64(b) * int64(i+1)
	}
	return 1 + int(sum%mod)
}

// Pos 返回第 j 个哈希位置 (h1 + j·h2) mod m，j 从 0 起。
func Pos(x []byte, m, j int) int {
	return (H1(x, m) + j*H2(x, m)) % m
}

// Positions 返回全部 k 个位置 pos_0..pos_{k-1}。
func Positions(x []byte, m, k int) []int {
	h1, h2 := H1(x, m), H2(x, m)
	pos := make([]int, k)
	for j := 0; j < k; j++ {
		pos[j] = (h1 + j*h2) % m
	}
	return pos
}
