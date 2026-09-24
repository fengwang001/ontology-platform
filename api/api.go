// Package api 是非对齐检查点算子的对外封装。
package api

import (
	"errors"
	"math/rand"

	"ontology/uck"
)

// View 是算子当前状态：累计和、最近处理值及存在位、各通道未处理条数。
type View struct {
	uck.Snap
	Pending [3]int
}

// API 是算子的对外句柄，所有方法可并发调用。
type API struct{ op *uck.Operator }

// New 创建算子，maxChannelState 为任一通道通道状态条数上限。
func New(maxChannelState int) *API { return &API{op: uck.New(maxChannelState)} }

func (a *API) Arrive(ch, v int) error  { return a.op.Arrive(ch, v) }
func (a *API) Step(ch int) error       { return a.op.Step(ch) }
func (a *API) Barrier(ch, n int) error { return a.op.Barrier(ch, n) }
func (a *API) Restore()                { a.op.Restore() }
func (a *API) RunAll()                 { a.op.RunAll() }

// State 返回当前算子状态。
func (a *API) State() View {
	s, p := a.op.State()
	return View{s, p}
}

// Snapshot 返回最近完成检查点的快照、两通道的通道状态与编号。
func (a *API) Snapshot() (View, []int, []int, int) {
	s, c1, c2, n := a.op.Snapshot()
	return View{Snap: s}, c1, c2, n
}

// Check 是一条自检结果。
type Check struct {
	Name string
	Err  error
}

// SelfCheck 对内置操作序列核验四条不变量，全部通过时各 Err 为 nil。
func (a *API) SelfCheck() []Check {
	inv1, inv2, inv3 := driveRandom(291)
	ok := []bool{inv1, inv2, inv3, checkReject()}
	names := []string{"不变量1 任意交错下与对齐式参照一致", "不变量2 恰好一次", "不变量3 恢复等价", "不变量4 失败不留痕"}
	out := make([]Check, 4)
	for i := range out {
		out[i].Name = names[i]
		if !ok[i] {
			out[i].Err = errors.New("自检失败: " + names[i])
		}
	}
	return out
}

func sumOf(xs []int) (s int) {
	for _, x := range xs {
		s += x
	}
	return s
}

// driveRandom 双算子重放同一随机序列（b 每个屏障后注入 Restore），核验不变量 1/2/3。
func driveRandom(seed int64) (inv1, inv2, inv3 bool) {
	a, b := New(1<<30), New(1<<30)
	r := rand.New(rand.NewSource(seed))
	inv1, inv2 = true, true
	var preSum, preCnt, preStep [3]int // 累计：屏障前到达和/条数、快照前已处理条数
	var frozen [3]bool                 // 各通道是否已收到本检查点屏障（即检查点进行中）
	for n := 1; n <= 30; n++ {
		for i := 0; i < 4+r.Intn(10); i++ {
			c := 1 + r.Intn(2)
			if r.Intn(3) == 0 {
				if a.Step(c) == nil && !frozen[1] && !frozen[2] {
					preStep[c]++
				}
				b.Step(c)
			} else {
				v := r.Intn(50)
				if a.Arrive(c, v) == nil && !frozen[c] {
					preSum[c] += v
					preCnt[c]++
				}
				b.Arrive(c, v)
			}
		}
		f := 1 + r.Intn(2)
		for _, c := range [2]int{f, 3 - f} {
			a.Barrier(c, n)
			b.Barrier(c, n)
			b.Restore()
			frozen[c] = true
		}
		snap, cs1, cs2, dn := a.Snapshot()
		if dn != n {
			return false, false, false
		}
		for c, cs := range map[int][]int{1: cs1, 2: cs2} {
			if snap.Sum[c]+sumOf(cs) != preSum[c] {
				inv1 = false
			}
			if len(cs) != preCnt[c]-preStep[c] {
				inv2 = false
			}
		}
		frozen = [3]bool{}
	}
	b.Restore()
	a.RunAll()
	b.RunAll()
	return inv1, inv2, a.State() == b.State()
}

// checkReject 四类故障注入：错误可判定且互不相同、被拒后状态不变、仍可继续用。
func checkReject() bool {
	a := New(2)
	same := func(do func() error, want error) bool {
		s := a.State()
		return errors.Is(do(), want) && a.State() == s
	}
	if !same(func() error { return a.Arrive(0, 1) }, uck.ErrChannel) ||
		!same(func() error { return a.Step(3) }, uck.ErrChannel) ||
		!same(func() error { return a.Step(1) }, uck.ErrEmpty) ||
		!same(func() error { return a.Barrier(1, 2) }, uck.ErrBarrier) {
		return false
	}
	if a.Arrive(1, 5) != nil || a.Barrier(1, 1) != nil ||
		!same(func() error { return a.Barrier(1, 1) }, uck.ErrBarrier) || // 同通道重复
		!same(func() error { return a.Barrier(2, 2) }, uck.ErrBarrier) || // 未完成时更大编号
		a.Arrive(2, 1) != nil || a.Arrive(2, 2) != nil ||
		!same(func() error { return a.Arrive(2, 3) }, uck.ErrStateLimit) { // 通道状态超限
		return false
	}
	b := New(1) // Barrier 时初始通道状态超限，整体拒绝
	if b.Arrive(1, 1) != nil || b.Arrive(1, 2) != nil ||
		!errors.Is(b.Barrier(1, 1), uck.ErrStateLimit) {
		return false
	}
	return a.Barrier(2, 1) == nil && b.Step(1) == nil && b.Barrier(1, 1) == nil // 被拒后仍可继续用
}
