// Package api 是对外门面：固定上限 k，校验输入，委托 dist/ops 计算。
package api

import (
	"errors"

	"ontology/dist"
	"ontology/ops"
)

// 三类可判定错误，互不相同。
var (
	ErrNegativeK = errors.New("api: negative cap k")
	ErrTooLong   = errors.New("api: input exceeds maxLen")
	// ErrExceedsCap 与 dist.ErrExceedsCap 是同一哨兵。
	ErrExceedsCap = dist.ErrExceedsCap
)

// maxLen 是 len(a)+len(b) 的上限。
const maxLen = 1 << 20

// Calculator 持有固定上限 k；创建后不可变，方法可并发调用。
type Calculator struct {
	k int
}

// New 校验并固定上限 k；k 为负时整体失败，不产生任何状态。
func New(k int) (*Calculator, error) {
	if k < 0 {
		return nil, ErrNegativeK
	}
	return &Calculator{k: k}, nil
}

// Op 与 ops.Op 相同，重新导出便于调用方使用。
type Op = ops.Op

// Distance 返回最小编辑代价；超上限返回 ErrExceedsCap，输入超长返回 ErrTooLong。
// 被拒绝时不改变任何可观察状态（Calculator 不可变，工作区按调用独立）。
func (c *Calculator) Distance(a, b string) (int, error) {
	if len(a)+len(b) > maxLen {
		return 0, ErrTooLong
	}
	return dist.Distance(a, b, c.k)
}

// EditScript 返回一条最小编辑脚本，长度恰等于 Distance 的返回值。
func (c *Calculator) EditScript(a, b string) ([]Op, error) {
	if len(a)+len(b) > maxLen {
		return nil, ErrTooLong
	}
	return ops.EditScript(a, b, c.k)
}

// SelfCheck 对内置字符串对核验四条不变量，全部通过返回 nil。
func (c *Calculator) SelfCheck() error {
	pairs := [][2]string{
		{"", ""}, {"", "abc"}, {"abc", ""}, {"abc", "yabd"},
		{"kitten", "sitting"}, {"flaw", "lawn"}, {"abc", "abc"},
		{"aaaa", "bbbb"}, {"abcdef", "azced"},
	}
	for _, p := range pairs {
		a, b := p[0], p[1]
		// 不变量 1：与朴素全表 DP 一致；不变量 3：上限判定恰好落在真实距离上。
		want := naive(a, b)
		d, err := c.Distance(a, b)
		if want <= c.k && (err != nil || d != want) {
			return errors.New("selfcheck: distance mismatch vs naive")
		}
		if want > c.k && err != ErrExceedsCap {
			return errors.New("selfcheck: over-cap not reported")
		}
		// 不变量 2：脚本应用后 a 变 b，长度恰等于距离。
		if want <= c.k {
			sc, err := c.EditScript(a, b)
			if err != nil || len(sc) != d {
				return errors.New("selfcheck: script length != distance")
			}
			out, err := ops.Apply(a, sc)
			if err != nil || out != b {
				return errors.New("selfcheck: script does not transform a into b")
			}
		}
		// 不变量 4：一次超限拒绝后，后续计算不受影响。
		if len(a)+len(b)+c.k+2 <= maxLen {
			if _, err := c.Distance(a, b+string(make([]byte, c.k+2))); err != ErrExceedsCap {
				return errors.New("selfcheck: expected ErrExceedsCap")
			}
			if d2, err := c.Distance(a, b); (want <= c.k && (err != nil || d2 != want)) || (want > c.k && err != ErrExceedsCap) {
				return errors.New("selfcheck: state changed after rejection")
			}
		}
	}
	return nil
}

// naive 是不加 band 的完整 O(n·m) DP，作为对照基准。
func naive(a, b string) int {
	n, m := len(a), len(b)
	prev := make([]int, m+1)
	cur := make([]int, m+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= n; i++ {
		cur[0] = i
		for j := 1; j <= m; j++ {
			best := prev[j] + 1
			if v := cur[j-1] + 1; v < best {
				best = v
			}
			sub := prev[j-1]
			if a[i-1] != b[j-1] {
				sub++
			}
			if sub < best {
				best = sub
			}
			cur[j] = best
		}
		prev, cur = cur, prev
	}
	return prev[m]
}
