// Package pfx 计算 KMP 前缀函数 π，并提供「当前态 j 消费一个字节后的推进」纯函数。
// 不依赖工程内其他包。
package pfx

// Compute 返回模式串 p 的前缀函数 pi：
// pi[i] = 子串 p[0..i] 的最长真前缀（长度 < i+1）且同时是其后缀的长度；pi[0]=0。
// p 为空时返回空切片。
func Compute(p []byte) []int {
	pi := make([]int, len(p))
	for i := 1; i < len(p); i++ {
		j := pi[i-1]
		for j > 0 && p[i] != p[j] {
			j = pi[j-1]
		}
		if p[i] == p[j] {
			j++
		}
		pi[i] = j
	}
	return pi
}

// Advance 返回匹配态 j 消费字节 c 后的新匹配态。
// 约定调用时 0 <= j < len(p)；返回值为 len(p) 表示在 c 处完成一次完整匹配，
// 是否回退到 pi[len(p)-1] 继续找重叠匹配由调用方决定。
func Advance(p []byte, pi []int, j int, c byte) int {
	for j > 0 && p[j] != c {
		j = pi[j-1]
	}
	if p[j] == c {
		j++
	}
	return j
}
