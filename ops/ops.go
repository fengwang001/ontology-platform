// Package ops 从带 band 的 DP 表回溯出最小编辑脚本。
package ops

import (
	"errors"

	"ontology/dist"
)

// ErrBadScript 表示脚本与源串不匹配（Apply 校验失败）。
var ErrBadScript = errors.New("ops: script does not apply to source")

// Kind 是原子编辑操作的种类。
type Kind byte

const (
	Ins Kind = iota // 在 Pos 处插入 Byte（不消耗 a）
	Del             // 删除 a[Pos]
	Rep             // 把 a[Pos] 替换为 Byte
)

// Op 是一条原子操作；Pos 相对原始串 a 的下标（Ins 可取 len(a) 表示追加）。
type Op struct {
	Kind Kind
	Pos  int
	Byte byte
}

// EditScript 返回把 a 变成 b 的一条最小编辑脚本；真实距离 > k 时返回 dist.ErrExceedsCap。
// 脚本长度恰等于 dist.Distance(a, b, k)。
func EditScript(a, b string, k int) ([]Op, error) {
	d, err := dist.Distance(a, b, k)
	if err != nil {
		return nil, err
	}
	n, m := len(a), len(b)
	// 只存带宽 |i-j| <= k 内的单元；最优路径不会越出带宽。
	const inf = 1 << 30
	rowLo := make([]int, n+1)
	tab := make([][]int, n+1)
	for i := 0; i <= n; i++ {
		lo := i - k
		if lo < 0 {
			lo = 0
		}
		hi := i + k
		if hi > m {
			hi = m
		}
		rowLo[i] = lo
		tab[i] = make([]int, hi-lo+1)
		for j := lo; j <= hi; j++ {
			switch {
			case i == 0:
				tab[i][j-lo] = j
			case j == 0:
				tab[i][j-lo] = i
			default:
				best := at(tab, rowLo, i-1, j, inf) + 1
				if v := at(tab, rowLo, i, j-1, inf) + 1; v < best {
					best = v
				}
				sub := at(tab, rowLo, i-1, j-1, inf)
				if a[i-1] != b[j-1] {
					sub++
				}
				if sub < best {
					best = sub
				}
				tab[i][j-lo] = best
			}
		}
	}
	// 从 (n,m) 回溯到 (0,0)，逆序收集操作后反转。
	rev := make([]Op, 0, d)
	for i, j := n, m; i > 0 || j > 0; {
		cur := at(tab, rowLo, i, j, inf)
		switch {
		case i > 0 && j > 0 && cur == at(tab, rowLo, i-1, j-1, inf)+subCost(a[i-1], b[j-1]):
			if a[i-1] != b[j-1] {
				rev = append(rev, Op{Rep, i - 1, b[j-1]})
			}
			i, j = i-1, j-1
		case i > 0 && cur == at(tab, rowLo, i-1, j, inf)+1:
			rev = append(rev, Op{Del, i - 1, 0})
			i--
		default: // j > 0 && cur == at(i, j-1)+1
			rev = append(rev, Op{Ins, i, b[j-1]})
			j--
		}
	}
	for l, r := 0, len(rev)-1; l < r; l, r = l+1, r-1 {
		rev[l], rev[r] = rev[r], rev[l]
	}
	return rev, nil
}

func subCost(x, y byte) int {
	if x == y {
		return 0
	}
	return 1
}

// at 读带宽表，越出带宽返回 inf。
func at(tab [][]int, rowLo []int, i, j, inf int) int {
	if i < 0 || j < 0 || i >= len(tab) {
		return inf
	}
	idx := j - rowLo[i]
	if idx < 0 || idx >= len(tab[i]) {
		return inf
	}
	return tab[i][idx]
}

// Apply 把脚本应用到 a 上，返回结果串；脚本与 a 不匹配时返回 ErrBadScript。
func Apply(a string, script []Op) (string, error) {
	out := make([]byte, 0, len(a)+len(script))
	oi := 0
	for i := 0; i <= len(a); i++ {
		for oi < len(script) && script[oi].Pos == i && script[oi].Kind == Ins {
			out = append(out, script[oi].Byte)
			oi++
		}
		if i == len(a) {
			break
		}
		if oi < len(script) && script[oi].Pos == i && script[oi].Kind != Ins {
			if script[oi].Kind == Rep {
				out = append(out, script[oi].Byte)
			}
			oi++
		} else {
			out = append(out, a[i])
		}
	}
	if oi != len(script) {
		return "", ErrBadScript
	}
	return string(out), nil
}
