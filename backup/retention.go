package backup

// rec 是已登记备份的内部记录。
type rec struct {
	id        string
	parent    string // 全量备份为空串
	size      int64
	createdAt int64
	corrupted bool
	held      bool
}

// planStats 记录计划引擎的关键步数，用于以可验证方式证明线性复杂度。
type planStats struct {
	recoverabilityVisits int
	propagationVisits    int
}

// computePlan 由当前已登记备份集合、保留策略与当前时刻计算清理计划。
// 前置条件：order 恰好包含 recs 中全部存活备份，按登记顺序排列
// （父备份一定先于子备份，且创建时刻非递减）。
// 总开销与备份总数及链深度之和成线性关系。
func computePlan(recs map[string]*rec, order []string, pol Policy, now int64, st *planStats) Plan {
	if st == nil {
		st = &planStats{}
	}
	recoverable := computeRecoverable(recs, order, st)
	direct := selectDirect(recs, order, recoverable, pol, now)
	return assemblePlan(recs, order, direct, st)
}

// computeRecoverable 计算每个备份是否可恢复：自身未被标记损坏，
// 且依赖链上所有祖先均未损坏。沿链向上行走并记忆化，
// 每个备份只被解析一次，总开销 O(备份数 + 链深度之和)。
// 采用迭代而非递归，避免深链爆栈。
func computeRecoverable(recs map[string]*rec, order []string, st *planStats) map[string]bool {
	memo := make(map[string]bool, len(recs))
	var stack []string
	for _, id := range order {
		if _, ok := memo[id]; ok {
			continue
		}
		// 沿父链向上，直到遇到已解析节点、根或损坏节点。
		cur := id
		for {
			if _, ok := memo[cur]; ok {
				break
			}
			r := recs[cur]
			stack = append(stack, cur)
			if r.corrupted || r.parent == "" {
				break
			}
			cur = r.parent
		}
		// 自顶向下回填：栈顶最接近已解析位置。
		for len(stack) > 0 {
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			st.recoverabilityVisits++
			r := recs[top]
			switch {
			case r.corrupted:
				memo[top] = false
			case r.parent == "":
				memo[top] = true
			default:
				memo[top] = memo[r.parent]
			}
		}
	}
	return memo
}

// layerSpec 描述一个保留层：周期序号函数、保留数量与对应原因位。
type layerSpec struct {
	index func(int64) int64
	n     int
	bit   Reasons
}

// selectDirect 对每一层，在最近 N 个周期（含当前周期）内，
// 每个周期各自选出一个可恢复的代表：创建时刻最晚者当选，
// 时刻相同则标识字典序较大者当选。返回直接保留原因位。
func selectDirect(recs map[string]*rec, order []string, recoverable map[string]bool, pol Policy, now int64) map[string]Reasons {
	layers := []layerSpec{
		{dayIndex, pol.Daily, ReasonDaily},
		{weekIndex, pol.Weekly, ReasonWeekly},
		{monthIndex, pol.Monthly, ReasonMonthly},
	}
	direct := make(map[string]Reasons)
	for _, layer := range layers {
		if layer.n <= 0 {
			continue
		}
		// 每个周期内可恢复备份中的最优者。
		best := make(map[int64]string)
		for _, id := range order {
			if !recoverable[id] {
				continue
			}
			r := recs[id]
			p := layer.index(r.createdAt)
			if cur, ok := best[p]; !ok || beats(r, recs[cur]) {
				best[p] = id
			}
		}
		c := layer.index(now)
		lo := c - int64(layer.n) + 1
		for p, id := range best {
			if p >= lo && p <= c {
				direct[id] |= layer.bit
			}
		}
	}
	return direct
}

// beats 判断代表竞选中 a 是否优于 b：创建时刻更晚，或时刻相同而标识更大。
func beats(a, b *rec) bool {
	if a.createdAt != b.createdAt {
		return a.createdAt > b.createdAt
	}
	return a.id > b.id
}

// assemblePlan 汇总保留原因并生成确定顺序的计划。
//
// 原因传播沿依赖链反向（子 -> 父）进行，利用登记顺序即拓扑序：
// 逆序遍历保证处理父备份时其全部子备份已处理，每个备份只访问一次。
//   - 自身或后代被法律保留 -> ReasonLegalHold（法律保留不受可恢复性限制）。
//   - 后代被直接保留       -> ReasonDependency（依赖保护）。
func assemblePlan(recs map[string]*rec, order []string, direct map[string]Reasons, st *planStats) Plan {
	legalDown := make(map[string]bool, len(recs)) // 自身或后代被法律保留
	depDown := make(map[string]bool, len(recs))   // 自身或后代被直接保留
	legalAnc := make(map[string]bool, len(recs))  // 是被法律保留备份的祖先
	depAnc := make(map[string]bool, len(recs))    // 是被直接保留备份的祖先
	for i := len(order) - 1; i >= 0; i-- {
		st.propagationVisits++
		id := order[i]
		r := recs[id]
		if r.held {
			legalDown[id] = true
		}
		if direct[id] != 0 {
			depDown[id] = true
		}
		if r.parent != "" {
			if legalDown[id] {
				legalDown[r.parent] = true
				legalAnc[r.parent] = true
			}
			if depDown[id] {
				depDown[r.parent] = true
				depAnc[r.parent] = true
			}
		}
	}

	var retained []RetainedEntry
	var deletable []string
	for _, id := range order {
		r := recs[id]
		reasons := direct[id]
		if r.held || legalAnc[id] {
			reasons |= ReasonLegalHold
		}
		if depAnc[id] {
			reasons |= ReasonDependency
		}
		if reasons == 0 {
			deletable = append(deletable, id)
		} else {
			retained = append(retained, RetainedEntry{ID: id, Reasons: reasons})
		}
	}

	// order 按创建时刻非递减，过滤保持该性质；只需在等时刻段内按标识排序，
	// 从而避免对全量备份做 O(n log n) 排序。
	sortRetainedRuns(recs, retained)
	sortIDRuns(recs, deletable)
	return Plan{Deletable: deletable, Retained: retained}
}

// runBounds 在创建时刻非递减的序列上，逐段找出等时刻区间并按标识排序。
func runBounds(n int, createdAt func(i int) int64, swap func(i, j int), lessID func(i, j int) bool) {
	lo := 0
	for lo < n {
		hi := lo + 1
		for hi < n && createdAt(hi) == createdAt(lo) {
			hi++
		}
		// 插入排序：等时刻段通常极短；最坏情况退化为段长平方，
		// 但不引入对备份总数的对数因子之外的链遍历。
		for i := lo + 1; i < hi; i++ {
			for j := i; j > lo && lessID(j, j-1); j-- {
				swap(j, j-1)
			}
		}
		lo = hi
	}
}

func sortRetainedRuns(recs map[string]*rec, entries []RetainedEntry) {
	runBounds(len(entries),
		func(i int) int64 { return recs[entries[i].ID].createdAt },
		func(i, j int) { entries[i], entries[j] = entries[j], entries[i] },
		func(i, j int) bool { return entries[i].ID < entries[j].ID })
}

func sortIDRuns(recs map[string]*rec, ids []string) {
	runBounds(len(ids),
		func(i int) int64 { return recs[ids[i]].createdAt },
		func(i, j int) { ids[i], ids[j] = ids[j], ids[i] },
		func(i, j int) bool { return ids[i] < ids[j] })
}
