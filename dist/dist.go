// Package dist 计算带相邻转置的无限制编辑距离（按码点）。
package dist

import (
	"errors"
	"unicode/utf8"
)

// ErrLimitExceeded 在码点数乘积超过上限时返回。
var ErrLimitExceeded = errors.New("dist: rune product exceeds limit")

// DefaultLimit 是 MaxProduct 传 0 时使用的默认上限。
const DefaultLimit = 1 << 26

var cells int

// Cells 返回最近一次 Compute 填充的 DP 单元格数。
func Cells() int { return cells }

// Result 保存一次距离计算的 DP 表与转置回溯信息。
type Result struct {
	dist int
	d    [][]int
	tr   [][2]int
	a, b []rune
}

// Dist 返回编辑距离。
func (r *Result) Dist() int { return r.dist }

// D 返回 d[i][j]。
func (r *Result) D(i, j int) int { return r.d[i][j] }

// Trans 返回格子 (i,j) 的转置锚点 (i1,j1)，无转置候选时为 (0,0)。
func (r *Result) Trans(i, j int) (int, int) {
	t := r.tr[i*(len(r.b)+1)+j]
	return t[0], t[1]
}

// Runes 返回源串与目标串的码点序列。
func (r *Result) Runes() ([]rune, []rune) { return r.a, r.b }

// Compute 计算 a 到 b 的编辑距离并返回可回溯的结果。
// maxProduct 为两串码点数乘积上限，0 表示使用 DefaultLimit。
// 上限检查在任何矩阵分配之前完成。
func Compute(a, b string, maxProduct int) (*Result, error) {
	ra, rb := Decode(a), Decode(b)
	m, n := len(ra), len(rb)
	if maxProduct <= 0 {
		maxProduct = DefaultLimit
	}
	if m*n > maxProduct {
		return nil, ErrLimitExceeded
	}
	// 「上一次出现位置」表按码点索引，条目数至多为源串/目标串中
	// 不同码点数，不随外部字母表大小增长。
	da := make(map[rune]int)
	w := n + 1
	d := make([][]int, m+1)
	for i := range d {
		d[i] = make([]int, w)
	}
	tr := make([][2]int, (m+1)*w)
	for i := 0; i <= m; i++ {
		d[i][0] = i
	}
	for j := 0; j <= n; j++ {
		d[0][j] = j
	}
	cells = 0
	for i := 1; i <= m; i++ {
		j1 := 0 // 当前行字符 a[i] 在 b 中上次出现的列
		for j := 1; j <= n; j++ {
			cells++
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			best := d[i-1][j] + 1
			if v := d[i][j-1] + 1; v < best {
				best = v
			}
			if v := d[i-1][j-1] + cost; v < best {
				best = v
			}
			// 无限制转置：允许转置后继续编辑两锚点之间的字符。
			i1 := da[rb[j-1]]
			if i1 > 0 && j1 > 0 {
				v := d[i1-1][j1-1] + (i - i1 - 1) + (j - j1 - 1) + 1
				if v < best {
					best = v
				}
				tr[i*w+j] = [2]int{i1, j1}
			}
			d[i][j] = best
			if ra[i-1] == rb[j-1] {
				j1 = j
			}
		}
		da[ra[i-1]] = i
	}
	return &Result{dist: d[m][n], d: d, tr: tr, a: ra, b: rb}, nil
}

// Distance 只返回距离值。
func Distance(a, b string, maxProduct int) (int, error) {
	r, err := Compute(a, b, maxProduct)
	if err != nil {
		return 0, err
	}
	return r.Dist(), nil
}

// Decode 把字符串解码为码点序列；每个非法 UTF-8 字节映射为
// 互不相同的合成码点 -1-byte。
func Decode(s string) []rune {
	rs := make([]rune, 0, len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			// 不同的非法字节必须视为不同码点：用负值合成码点区分。
			r = -1 - rune(s[i])
		}
		rs = append(rs, r)
		i += size
	}
	return rs
}

// Encode 把 Decode 产出的码点序列还原为字符串。
func Encode(rs []rune) string {
	buf := make([]byte, 0, len(rs))
	for _, r := range rs {
		if r < 0 {
			buf = append(buf, byte(-1-r))
			continue
		}
		var e [4]byte
		size := utf8.EncodeRune(e[:], r)
		buf = append(buf, e[:size]...)
	}
	return string(buf)
}
