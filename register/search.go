package register

import "sort"

// state 是 DFS 的备忘录键：已放置集合 + 当前寄存器值。
type state struct {
	placed uint32
	value  int
}

// search 在快照上深搜见证序。
//
// 每步候选是尚未放置、且不存在任何尚未放置的已结束操作
// 其返回时刻严格小于候选调用时刻的操作；按（调用时刻，编号）
// 升序逐个尝试，能放入就递归。已结束操作全部放完即成功，
// 返回首个成功的放置顺序（含被放入的未结束操作）。
// 同一（已放置集合，当前值）失败过则不再展开。
func search(ops []opRecord) (bool, []int) {
	n := len(ops)
	finishedMask := uint32(0)
	for i := range ops {
		if ops[i].finished {
			finishedMask |= 1 << uint(i)
		}
	}

	// 候选尝试顺序：按（调用时刻，编号）升序。
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool {
		oa, ob := ops[order[a]], ops[order[b]]
		if oa.invoke != ob.invoke {
			return oa.invoke < ob.invoke
		}
		return oa.id < ob.id
	})

	failed := make(map[state]struct{})
	var path []int

	var dfs func(placed uint32, value int) bool
	dfs = func(placed uint32, value int) bool {
		if placed&finishedMask == finishedMask {
			return true
		}
		st := state{placed: placed, value: value}
		if _, ok := failed[st]; ok {
			return false
		}
		// 尚未放置的已结束操作的最小返回时刻；
		// 候选的调用时刻不得严格大于它（相等视为并发）。
		minRet := 0
		hasMin := false
		for i := range ops {
			if !ops[i].finished || placed&(1<<uint(i)) != 0 {
				continue
			}
			if !hasMin || ops[i].ret < minRet {
				minRet = ops[i].ret
				hasMin = true
			}
		}
		for _, i := range order {
			if placed&(1<<uint(i)) != 0 {
				continue
			}
			rec := ops[i]
			if hasMin && minRet < rec.invoke {
				continue
			}
			next, ok := apply(rec, value)
			if !ok {
				continue
			}
			path = append(path, rec.id)
			if dfs(placed|(1<<uint(i)), next) {
				return true
			}
			path = path[:len(path)-1]
		}
		failed[st] = struct{}{}
		return false
	}

	if dfs(0, 0) {
		return true, path
	}
	return false, nil
}

// apply 尝试把一条操作记录放入序列，返回放入后的寄存器值。
// 已结束操作按其实际结果校验寄存器语义；
// 未结束的写可放入并生效；未结束的 CAS 仅在当前值等于期望时
// 可放入并生效；未结束的读不放入。
func apply(rec opRecord, value int) (int, bool) {
	switch rec.op.Kind {
	case Write:
		return rec.op.V, true
	case Read:
		if !rec.finished {
			return value, false
		}
		if *rec.res.Value != value {
			return value, false
		}
		return value, true
	case CAS:
		if rec.finished && !*rec.res.Success {
			// 失败的 CAS：当前值必须不等于期望值，且保持不变。
			if value == rec.op.E {
				return value, false
			}
			return value, true
		}
		// 成功的或未结束的 CAS：当前值必须等于期望值，随后置为新值。
		if value != rec.op.E {
			return value, false
		}
		return rec.op.N, true
	}
	return value, false
}
