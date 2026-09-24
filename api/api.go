// Package api 是对外门面：New(P)、各统计操作的包装与 SelfCheck()。
// 依赖方向：api -> route -> pofs，单向。
package api

import (
	"errors"
	"fmt"

	"ontology/route"
)

// 三类可判定哨兵错误（与 route 同源，互不相同的值）。
var (
	ErrPartOutOfRange = route.ErrPartOutOfRange
	ErrGap            = route.ErrGap
	ErrNegative       = route.ErrNegative
)

// Router 是分区保序路由器的对外句柄。
type Router struct{ r *route.Router }

// New 创建 P 个分区的路由器。
func New(P int) *Router { return &Router{r: route.NewRouter(P)} }

// Append 接收一条事件；Part 越界 / Val 为负 / Pos 不连续分别返回三类哨兵错误，失败零改动。
func (a *Router) Append(part int, pos, val int64) error { return a.r.Append(part, pos, val) }

// W 返回水位 min(各分区计数)。
func (a *Router) W() int { return a.r.W() }

// PrefixTotal 返回全局已提交前缀的 Val 之和。
func (a *Router) PrefixTotal() int64 { return a.r.PrefixTotal() }

// Total 返回全部已接受事件的 Val 之和。
func (a *Router) Total() int64 { return a.r.Total() }

// PartCount 返回分区 p 已接受事件数。
func (a *Router) PartCount(p int) int { return a.r.PartCount(p) }

// PartSum 返回分区 p 的 Val 之和。
func (a *Router) PartSum(p int) int64 { return a.r.PartSum(p) }

// ev 是内置自检用事件。
type ev struct {
	part int
	pos  int64
	val  int64
}

// nine 是第三节 P=2 的九步事件序列。
var nine = []ev{
	{0, 0, 10}, {1, 0, 20}, {0, 1, 30}, {0, 2, 40}, {1, 1, 50},
	{0, 3, 60}, {1, 2, 70}, {1, 3, 80}, {0, 4, 90},
}

// nineExpect 是逐步 [W, PrefixTotal] 的推导结果（见 NOTES.md）。
var nineExpect = [][2]int64{
	{0, 0}, {1, 30}, {1, 30}, {1, 30}, {2, 110},
	{2, 110}, {3, 220}, {4, 360}, {4, 360},
}

// naive 朴素参照：按分区收集后各自取前 w 条求和。
func naive(events []ev, w int) (total, prefix int64) {
	per := map[int][]int64{}
	for _, e := range events {
		total += e.val
		per[e.part] = append(per[e.part], e.val)
	}
	for _, vals := range per {
		for i := 0; i < w && i < len(vals); i++ {
			prefix += vals[i]
		}
	}
	return total, prefix
}

// SelfCheck 对内置事件序列核验四条不变量，全部通过返回 nil。
func SelfCheck() error {
	a := New(2)
	var pw, ppt, pt int64 = 0, 0, 0
	for i, e := range nine {
		if err := a.Append(e.part, e.pos, e.val); err != nil {
			return fmt.Errorf("selfcheck append %d: %w", i, err)
		}
		w, p, tt := int64(a.W()), a.PrefixTotal(), a.Total()
		if w != nineExpect[i][0] || p != nineExpect[i][1] {
			return fmt.Errorf("selfcheck step %d: got W=%d PT=%d", i+1, w, p)
		}
		if w < pw || p < ppt || tt < pt { // 不变量2：按位点单调
			return fmt.Errorf("selfcheck step %d: not monotone", i+1)
		}
		pw, ppt, pt = w, p, tt
	}
	// 不变量1：与朴素参照一致。
	if total, prefix := naive(nine, a.W()); a.Total() != total || a.PrefixTotal() != prefix {
		return errors.New("selfcheck: naive mismatch")
	}
	// 不变量3：另一种交错顺序（分区0全部先到）终值相同。
	b := New(2)
	for _, list := range [][]ev{nine} {
		for _, e := range list {
			if e.part == 0 {
				_ = b.Append(e.part, e.pos, e.val)
			}
		}
		for _, e := range list {
			if e.part == 1 {
				_ = b.Append(e.part, e.pos, e.val)
			}
		}
	}
	if b.W() != a.W() || b.PrefixTotal() != a.PrefixTotal() || b.Total() != a.Total() {
		return errors.New("selfcheck: interleave mismatch")
	}
	// 不变量4：三类失败互不相同、零改动、之后仍可用。
	w0, p0, t0 := a.W(), a.PrefixTotal(), a.Total()
	bads := []error{
		a.Append(2, 0, 1),  // Part 越界
		a.Append(0, 99, 1), // 位点不连续
		a.Append(0, 5, -1), // Val 为负
	}
	for i, want := range []error{ErrPartOutOfRange, ErrGap, ErrNegative} {
		if !errors.Is(bads[i], want) {
			return fmt.Errorf("selfcheck: bad[%d]=%v want %v", i, bads[i], want)
		}
	}
	if a.W() != w0 || a.PrefixTotal() != p0 || a.Total() != t0 {
		return errors.New("selfcheck: rejected op mutated state")
	}
	if err := a.Append(1, 4, 100); err != nil || a.W() != 5 {
		return errors.New("selfcheck: unusable after rejection")
	}
	return nil
}
