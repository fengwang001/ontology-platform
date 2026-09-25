// Package period 依据 Z 数组求字符串的最短周期。
package period

import "ontology/zfn"

// Shortest 返回 z 对应字符串的最短周期。
//
// p 是周期当且仅当 Z[p] == n-p（s 与 s[p:] 全程相等）。
// 自 p=1 起升序枚举到 n-1，第一个满足者即最短周期；
// 任何非平凡 p 都不满足时 p=n 恒成立，返回 n。
// 不要求 p 整除 n，也不做递减遍历。
func Shortest(z *zfn.Z) int {
	n := z.Len()
	for p := 1; p < n; p++ {
		if z.At(p) == n-p {
			return p
		}
	}
	return n
}
