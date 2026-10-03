package budget

import "math/bits"

// validateTask 检查任务参数：编号非空、1 <= C <= D <= T <= 1000。
func validateTask(task Task) bool {
	if task.ID == "" {
		return false
	}
	return task.C >= 1 && task.C <= task.D && task.D <= task.T && task.T <= 1000
}

// validatePi 检查组件周期：1 <= Pi <= 1000。
func validatePi(pi int64) bool { return pi >= 1 && pi <= 1000 }

// minBudgetResult 是 MinBudget 的完整结果，便于携带判定依据。
type minBudgetResult struct {
	theta    int64 // 最小可行预算；infeasible 时为 pi
	feasible bool
	violateT int64 // theta=pi 时最小违反点；仅因利用率>1 时为 0
	tooLarge bool  // Dmax+H 超过 sizeLimit
	horizon  int64 // Dmax+H
	points   []int64
}

// minBudget 求满足可调度条件的最小整数预算 theta（1..pi）。
// sbf 对 theta 单调，故使用二分；空任务集返回 0。
//
// 可行性检查次数不超过 ceil(log2(pi)) + 1：
// 二分区间 [1, pi] 至多 ceil(log2(pi)) 次，外加 theta=pi 的终检。
func minBudget(pi int64, tasks []Task) minBudgetResult {
	if len(tasks) == 0 {
		return minBudgetResult{theta: 0, feasible: true}
	}
	_, horizon, ok := checkHorizon(pi, tasks)
	if !ok {
		return minBudgetResult{tooLarge: true}
	}
	points := jumpPoints(tasks, horizon)
	res := minBudgetResult{horizon: horizon, points: points}

	// 终检：theta = pi 必须可行，否则任务集本身不可调度。
	if fullOK, badT := feasibleAt(pi, pi, tasks, points); !fullOK {
		res.feasible = false
		res.theta = pi
		if utilizationGT1(tasks) {
			// 仅因 sum C/T > 1 不可行时记 0。
			res.violateT = 0
		} else {
			// theta=pi 时 sbf(t)=t，违反点即最小的 dbf 跳变点。
			res.violateT = badT
		}
		return res
	}

	// 二分最小可行 theta（可行性对 theta 单调）。
	lo, hi := int64(1), pi
	for lo < hi {
		mid := lo + (hi-lo)/2
		if ok, _ := feasibleAt(pi, mid, tasks, points); ok {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	res.feasible = true
	res.theta = lo
	return res
}

// ceilLog2 返回 ceil(log2(n))，n >= 1。
func ceilLog2(n int64) int {
	if n <= 1 {
		return 0
	}
	b := bits.Len64(uint64(n - 1))
	return b
}
