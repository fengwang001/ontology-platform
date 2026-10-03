package budget

import "sync/atomic"

// feasibilityChecks 统计 feasibleAt 的调用次数，供测试断言二分检查次数
// 上界；非导出计数器，测试位于同一包内。
var feasibilityChecks atomic.Int64

// resetFeasibilityChecks 清零计数器（测试辅助）。
func resetFeasibilityChecks() { feasibilityChecks.Store(0) }

// utilizationOK 精确判断 sum(C_i/T_i) <= theta/pi，全程使用大整数，
// 不使用浮点。
func utilizationOK(tasks []Task, pi, theta int64) bool {
	// sum(C/T) <= theta/pi  <=>  pi * sum(C * Π_{j!=i} T_j) <= theta * Π T_j
	// 用 big.Rat 直接做精确有理数比较。
	sum := newRat0()
	for _, task := range tasks {
		sum.Add(sum, bigRat(task.C, task.T))
	}
	return sum.Cmp(bigRat(theta, pi)) <= 0
}

// utilizationGT1 精确判断 sum(C_i/T_i) > 1。
func utilizationGT1(tasks []Task) bool {
	sum := newRat0()
	for _, task := range tasks {
		sum.Add(sum, bigRat(task.C, task.T))
	}
	return sum.Cmp(bigRat1()) > 0
}

// feasibleAt 判断任务集在预算 theta 下是否可调度，并且当不可行时返回
// 最小的违反跳变点 t（utilization 失败记 0）。
//
// 可行当且仅当：
//  1. sum(C/T) <= theta/Pi（精确比较）；
//  2. 对全部 dbf 跳变点 t（0 < t <= Dmax+H），dbf(t) <= sbf(t)。
func feasibleAt(pi, theta int64, tasks []Task, points []int64) (bool, int64) {
	feasibilityChecks.Add(1)
	if !utilizationOK(tasks, pi, theta) {
		return false, 0
	}
	for _, t := range points {
		if dbf(tasks, t) > sbf(pi, theta, t) {
			return false, t
		}
	}
	return true, 0
}
