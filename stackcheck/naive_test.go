package stackcheck_test

// naiveAnalyze 是按题面规则独立写成的朴素 FIFO 推演，作为 Analyze 的对照神谕。
// 结构刻意与实现保持不同（用 map 工作表、辅助函数返回 (reason, pc, depth)）。
func naiveAnalyze(limit int, ops []naiveOp) (entry []int, maxDepth int, reason naiveReason, atPC, atDepth int) {
	bad := func(r naiveReason, pc, d int) ([]int, int, naiveReason, int, int) {
		return nil, 0, r, pc, d
	}
	if limit < 1 {
		return bad(nrInvalidLimit, -1, -1)
	}
	if len(ops) == 0 {
		return bad(nrEmpty, -1, -1)
	}

	n := len(ops)
	for i := 0; i < n; i++ {
		if !known(ops[i].code) {
			return bad(nrUnknownOp, i, -1)
		}
	}
	for i := 0; i < n; i++ {
		if (ops[i].code == ncJMP || ops[i].code == ncJZ) && (ops[i].target < 0 || ops[i].target >= n) {
			return bad(nrJumpOOB, i, -1)
		}
	}

	entry = make([]int, n)
	for i := range entry {
		entry[i] = -1
	}
	entry[0] = 0
	queue := []int{0}
	maxDepth = 0

	pop := func() int {
		pc := queue[0]
		queue = queue[1:]
		return pc
	}

	for len(queue) > 0 {
		pc := pop()
		d := entry[pc]
		code := ops[pc].code

		need := 0
		switch code {
		case ncPOP, ncJZ:
			need = 1
		case ncADD:
			need = 2
		case ncDUP:
			need = 1
		}
		if d < need {
			return bad(nrUnderflow, pc, d)
		}

		after := d
		switch code {
		case ncPUSH, ncDUP:
			after = d + 1
		case ncPOP, ncADD, ncJZ:
			after = d - 1
		}
		if after > limit {
			return bad(nrOverflow, pc, d)
		}
		if code == ncRET && d != 1 {
			return bad(nrBadReturn, pc, d)
		}
		if after > maxDepth {
			maxDepth = after
		}

		assign := func(next, depth int) bool {
			if entry[next] == -1 {
				entry[next] = depth
				queue = append(queue, next)
				return true
			}
			return entry[next] == depth
		}

		switch code {
		case ncRET:
		case ncJMP:
			if !assign(ops[pc].target, after) {
				return bad(nrMerge, ops[pc].target, after)
			}
		case ncJZ:
			if !assign(ops[pc].target, after) {
				return bad(nrMerge, ops[pc].target, after)
			}
			if pc+1 == n {
				return bad(nrFallthrough, pc, d)
			}
			if !assign(pc+1, after) {
				return bad(nrMerge, pc+1, after)
			}
		default:
			if pc+1 == n {
				return bad(nrFallthrough, pc, d)
			}
			if !assign(pc+1, after) {
				return bad(nrMerge, pc+1, after)
			}
		}
	}
	return entry, maxDepth, nrOK, -1, -1
}

type naiveReason string

const (
	nrOK           naiveReason = ""
	nrInvalidLimit naiveReason = "invalid_limit"
	nrEmpty        naiveReason = "empty"
	nrUnknownOp    naiveReason = "unknown"
	nrJumpOOB      naiveReason = "jump_oob"
	nrUnderflow    naiveReason = "underflow"
	nrOverflow     naiveReason = "overflow"
	nrMerge        naiveReason = "merge"
	nrBadReturn    naiveReason = "bad_return"
	nrFallthrough  naiveReason = "fallthrough"
)

type naiveOp struct {
	code   int
	target int
}

const (
	ncPUSH = 1
	ncPOP  = 2
	ncADD  = 3
	ncDUP  = 4
	ncJMP  = 5
	ncJZ   = 6
	ncRET  = 7
)

func known(code int) bool {
	return code >= ncPUSH && code <= ncRET
}
