package rta

import "math/bits"

// maxDemand 是内部饱和阈值：任何中间需求超过该值都视为不可调度。
// 合法任务的截止判定上界 D-J <= 1e9，远超该阈值的需求不可能回落。
const maxDemand int64 = 1_000_000_000_000_000_000

// demand 计算 w 处的工作量需求：
//
//	demand(w) = C_i + B_i + Σ_{j∈H} ceil((w + J_j)/T_j) * C_j
//
// 全程使用 128 位累加；超过 maxDemand 时 saturated=true。
func demand(w int64, target Task, higher []Task) (d int64, saturated bool) {
	var accHi, accLo uint64
	accLo = uint64(target.C + target.B) // C+B <= 2e9，无溢出
	for _, j := range higher {
		num := uint64(w + j.J) // w <= maxDemand，J <= 1e9，远小于 int64 上限
		t := uint64(j.T)
		q := (num + t - 1) / t
		prodHi, prodLo := bits.Mul64(q, uint64(j.C))
		var carry uint64
		accLo, carry = bits.Add64(accLo, prodLo, 0)
		accHi, _ = bits.Add64(accHi, prodHi, carry)
		if accHi != 0 || accLo > uint64(maxDemand) {
			return maxDemand, true
		}
	}
	return int64(accLo), false
}

// analyzeTask 以给定有序高优先级干扰任务集合，对 target 做 RTA 不动点迭代。
// 自 w=C+B 起迭代 w=d(w)；任何一个 w（含初值）满足 w+J>D 即失败；
// d 饱和（超过 maxDemand）同样失败。成功返回 R=w+J。
// steps 返回执行的"求和步"数（含初值检查那一步），每步恰对 len(higher)
// 个干扰任务各求一项。
func analyzeTask(target Task, higher []Task) (r int64, steps int, ok bool) {
	w := target.C + target.B
	for {
		steps++
		if w+target.J > target.D {
			return 0, steps, false
		}
		d, sat := demand(w, target, higher)
		if sat {
			return 0, steps, false
		}
		if d <= w {
			return w + target.J, steps, true
		}
		w = d
	}
}

// schedulableOrder 判断给定从高到低的完整次序是否全员可调度。
func schedulableOrder(tasks []Task) bool {
	for i := range tasks {
		if _, _, ok := analyzeTask(tasks[i], tasks[:i]); !ok {
			return false
		}
	}
	return true
}

// audsley 对任务集合运行 Audsley 算法。
//
// 自最低优先级位起：在未分配任务中，令其余全部未分配任务为高优先级集 H，
// 取其中可调度且编号字节序最小者占据该位；某位无人可调度则失败，
// unassigned 为此时尚未分配的任务个数（含正在填的那一位）。
// 候选可调度性只依赖 H 的集合，与 H 内部次序无关。
func audsley(tasks []Task) (order []Task, unassigned int, ok bool) {
	remaining := make([]Task, len(tasks))
	copy(remaining, tasks)
	lowToHigh := make([]Task, 0, len(remaining))
	for len(remaining) > 0 {
		picked := -1
		for i := range remaining {
			higher := make([]Task, 0, len(remaining)-1)
			higher = append(higher, remaining[:i]...)
			higher = append(higher, remaining[i+1:]...)
			if _, _, feasible := analyzeTask(remaining[i], higher); feasible {
				if picked == -1 || remaining[i].ID < remaining[picked].ID {
					picked = i
				}
			}
		}
		if picked == -1 {
			return nil, len(remaining), false
		}
		lowToHigh = append(lowToHigh, remaining[picked])
		remaining = append(remaining[:picked], remaining[picked+1:]...)
	}
	for i, j := 0, len(lowToHigh)-1; i < j; i, j = i+1, j-1 {
		lowToHigh[i], lowToHigh[j] = lowToHigh[j], lowToHigh[i]
	}
	return lowToHigh, 0, true
}
