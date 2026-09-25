// Package longest 由 d1/d2 半径求最长回文子串的起始与长度（等长取最左）。
// 依赖 manacher 的输出，不反向依赖。
package longest

// Find 返回最长回文子串的起始下标与长度。
// 奇回文长度 = 2·d1[i]-1，起始 = i-d1[i]+1；偶回文长度 = 2·d2[i]，起始 = i-d2[i]。
// 严格大于才更新且下标升序扫描，保证等长时取最靠左。
func Find(d1, d2 []int) (start, length int) {
	if len(d1) == 0 {
		return 0, 0
	}
	start, length = 0, -1
	for i, r := range d1 {
		if l := 2*r - 1; l > length {
			start, length = i-r+1, l
		}
	}
	for i, r := range d2 {
		if l := 2 * r; l > length {
			start, length = i-r, l
		}
	}
	return start, length
}
