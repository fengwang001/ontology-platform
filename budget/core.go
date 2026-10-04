package budget

import (
	"math/big"
	"sort"
)

const (
	// MaxComponents 为全局组件数上限。
	MaxComponents = 8
	// MaxTasksPerComponent 为单组件任务数上限。
	MaxTasksPerComponent = 8
	// MaxPeriod 为组件周期 Π 与任务参数的上界。
	MaxPeriod = 1000
	// MaxHorizon 为 Dmax+H 的规模上限，超过即报 RejectTooLarge。
	MaxHorizon = 1_000_000
)

// Task 描述组件内一个 EDF 任务：1<=C<=D<=T<=MaxPeriod，编号在组件内唯一。
type Task struct {
	ID string
	C  int64
	T  int64
	D  int64
}

func (t Task) valid() bool {
	return t.ID != "" && t.C >= 1 && t.C <= t.D && t.D <= t.T && t.T <= MaxPeriod
}

// sbf 为周期资源模型 (Π,Θ) 的最坏情形供给界：
// 令 a=Π-Θ，q=floor(max(0,t-a)/Π)，则 sbf(t)=q·Θ+max(0,t-2a-q·Π)；Θ=Π 时 sbf(t)=t。
func sbf(pi, theta, t int64) int64 {
	if theta == pi {
		return t
	}
	if t <= 0 {
		return 0
	}
	a := pi - theta
	q := int64(0)
	if t > a {
		q = (t - a) / pi
	}
	rem := t - 2*a - q*pi
	if rem < 0 {
		rem = 0
	}
	return q*theta + rem
}

// dbf 为 EDF 需求界：Σ max(0, floor((t-D)/T)+1)·C，t<D 的任务贡献 0。
func dbf(tasks []Task, t int64) int64 {
	var sum int64
	for _, task := range tasks {
		if t >= task.D {
			sum += ((t-task.D)/task.T + 1) * task.C
		}
	}
	return sum
}

// jumpPoints 返回 (0, bound] 内 dbf 的全部跳变点（各任务的 D+mT），升序去重。
// 返回的考察点数不超过 Σ⌈bound/T_i⌉。
func jumpPoints(tasks []Task, bound int64) []int64 {
	points := make([]int64, 0, len(tasks))
	for _, task := range tasks {
		for t := task.D; t <= bound; t += task.T {
			points = append(points, t)
		}
	}
	sort.Slice(points, func(i, j int) bool { return points[i] < points[j] })
	out := points[:0]
	for i, p := range points {
		if i == 0 || p != points[i-1] {
			out = append(out, p)
		}
	}
	return out
}

// horizon 计算 Dmax 与 H=lcm(Π, 全部 T)。任一中间值超过 MaxHorizon 即提前
// 停止并返回 ok=false（防溢出），调用方据此报 RejectTooLarge。
func horizon(pi int64, tasks []Task) (dmax, h int64, ok bool) {
	h = pi
	for _, task := range tasks {
		if task.D > dmax {
			dmax = task.D
		}
		g := gcd(h, task.T)
		// h/g <= MaxHorizon 且 T <= MaxPeriod，故 h/g*T <= 10^9，int64 不会溢出。
		h = h / g * task.T
		if h > MaxHorizon {
			return 0, 0, false
		}
	}
	if dmax+h > MaxHorizon {
		return 0, 0, false
	}
	return dmax, h, true
}

func gcd(a, b int64) int64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// feasible 检查任务集在预算 theta 下是否可调度：
// ΣC/T <= Θ/Π（有理数精确比较）且所有 dbf 跳变点 t∈(0,Dmax+H] 满足 dbf(t)<=sbf(t)。
// 调用前须保证规模未超限。返回最小违反点 t（0 表示可行或仅利用率违反）。
func feasible(pi, theta int64, tasks []Task, dmax, h int64) (ok bool, violationT int64) {
	stats.feasibilityChecks++
	if !utilizationLE(tasks, theta, pi) {
		return false, 0
	}
	points := jumpPoints(tasks, dmax+h)
	stats.jumpPointsChecked += int64(len(points))
	for _, t := range points {
		if dbf(tasks, t) > sbf(pi, theta, t) {
			return false, t
		}
	}
	return true, 0
}

// utilizationLE 精确比较 ΣC/T <= num/den（128 位 big.Int，不用浮点）。
// 等价于比较 Σ C_i·den·Π_{j≠i}T_j 与 num·Π_j T_j。
func utilizationLE(tasks []Task, num, den int64) bool {
	prod := big.NewInt(1)
	for _, task := range tasks {
		prod.Mul(prod, big.NewInt(task.T))
	}
	lhs := new(big.Int)
	for _, task := range tasks {
		term := new(big.Int).Mul(big.NewInt(task.C), big.NewInt(den))
		term.Mul(term, new(big.Int).Quo(prod, big.NewInt(task.T)))
		lhs.Add(lhs, term)
	}
	rhs := new(big.Int).Mul(big.NewInt(num), prod)
	return lhs.Cmp(rhs) <= 0
}

// 非导出统计计数器，仅供同包测试断言，不影响任何对外行为。
var stats struct {
	feasibilityChecks int64
	jumpPointsChecked int64
}

func resetStats() {
	stats.feasibilityChecks = 0
	stats.jumpPointsChecked = 0
}

// MinBudget 返回使任务集可调度（定义见 feasible）的最小整数预算 Θ∈[1,Π]。
// sbf 对 Θ 单调，故二分查找；可行性检查次数不超过 ⌈log2 Π⌉+1。
// Θ=Π 仍不可行时返回 ok=false，并携带 Θ=Π 时的最小违反点 t（仅利用率违反时为 0）。
// 任务集为空时返回 0。输入任务顺序不影响结果。
func MinBudget(pi int64, tasks []Task) (theta int64, ok bool, violationT int64) {
	if len(tasks) == 0 {
		return 0, true, 0
	}
	dmax, h, within := horizon(pi, tasks)
	if !within {
		// 调用方（Manager）已先做规模判定；此处防御性处理。
		return pi, false, 0
	}
	return minBudget(pi, tasks, dmax, h)
}

// minBudget 在已知 horizon 未超限的前提下执行二分。
func minBudget(pi int64, tasks []Task, dmax, h int64) (theta int64, ok bool, violationT int64) {
	// 先在 Θ=Π 处判定可行性，同时取得最小违反点。
	feasOk, vt := feasible(pi, pi, tasks, dmax, h)
	if !feasOk {
		return pi, false, vt
	}
	lo, hi := int64(1), pi // 不变式：hi 可行，lo-1 不可行（lo=1 时未验证）。
	for lo < hi {
		mid := lo + (hi-lo)/2
		if ok, _ := feasible(pi, mid, tasks, dmax, h); ok {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo, true, 0
}
