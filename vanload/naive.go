package vanload

// naiveOutcome 是朴素模型单件装货的判定结果。
type naiveOutcome struct {
	chosen  int
	overall RejectKind
	reasons map[int]RejectKind
	pre     RejectKind // 四类约束之前的拒绝原因（参数/编号/已过），KindNone 表示无
}

// naiveSystem 是独立编写的朴素参考模型：
// 直接保存每件货物，判定时枚举全部在车货物逐分区验证，
// 不使用任何聚合结构或前缀/后缀数组，用于与主系统做随机操作序列对照。
type naiveSystem struct {
	compartments []Compartment
	items        []naiveItem // 保留插入顺序，便于确定性比对
	arrivedStop  int
}

type naiveItem struct {
	cargo       Cargo
	compartment int
}

func newNaive(cmps []Compartment) *naiveSystem {
	cp := make([]Compartment, len(cmps))
	copy(cp, cmps)
	return &naiveSystem{compartments: cp}
}

func (n *naiveSystem) findID(id int) int {
	for i, it := range n.items {
		if it.cargo.ID == id {
			return i
		}
	}
	return -1
}

// precheck 模拟四类约束之前的三道检查。
func (n *naiveSystem) precheck(c Cargo) RejectKind {
	if !c.Valid() {
		return KindInvalidArgument
	}
	if n.findID(c.ID) >= 0 {
		return KindDuplicateID
	}
	if c.Stop <= n.arrivedStop {
		return KindStopPassed
	}
	return KindNone
}

// load 朴素装货判定：先做前置检查，再逐分区、逐货物枚举。
func (n *naiveSystem) load(c Cargo) naiveOutcome {
	if pre := n.precheck(c); pre != KindNone {
		return naiveOutcome{pre: pre}
	}
	reasons := make(map[int]RejectKind)
	chosen := 0
	for k := 1; k <= len(n.compartments); k++ {
		kind := n.tryCompartment(k, c)
		if kind == KindNone {
			if chosen == 0 {
				chosen = k
			}
			continue
		}
		reasons[k] = kind
	}
	out := naiveOutcome{chosen: chosen}
	if chosen == 0 {
		out.overall = mergeReasons(reasons)
		out.reasons = reasons
	}
	return out
}

// commit 把货物真正放入分区。
func (n *naiveSystem) commit(c Cargo, k int) {
	n.items = append(n.items, naiveItem{cargo: c, compartment: k})
}

// tryCompartment 按严重度顺序枚举在车货物，找出该分区首个失败约束。
func (n *naiveSystem) tryCompartment(k int, c Cargo) RejectKind {
	orderFailed := false
	isolationFailed := false
	weight := c.Weight
	volume := c.Volume

	for _, it := range n.items {
		// 顺序约束：任一在车货物违反跨分区相对位置即冲突。
		if it.compartment < k && it.cargo.Stop < c.Stop {
			orderFailed = true
		}
		if it.compartment > k && it.cargo.Stop > c.Stop {
			orderFailed = true
		}
		// 隔离约束：仅与同分区货物相关。
		if it.compartment == k && incompatible(c.Category, it.cargo.Category) {
			isolationFailed = true
		}
		// 载重/容积：累加同分区货物。
		if it.compartment == k {
			weight += it.cargo.Weight
			volume += it.cargo.Volume
		}
	}

	if orderFailed {
		return KindOrderConflict
	}
	if isolationFailed {
		return KindIsolationConflict
	}
	if weight > n.compartments[k-1].MaxWeight {
		return KindOverweight
	}
	if volume > n.compartments[k-1].MaxVolume {
		return KindOvervolume
	}
	return KindNone
}

// incompatible 判断两类货物是否不得同区。
// 易燃与氧化互斥；食品不与易燃、氧化任一类同区；其余组合允许。
func incompatible(a, b Category) bool {
	if (a == CategoryFlammable && b == CategoryOxidizing) ||
		(a == CategoryOxidizing && b == CategoryFlammable) {
		return true
	}
	if a == CategoryFood && (b == CategoryFlammable || b == CategoryOxidizing) {
		return true
	}
	if b == CategoryFood && (a == CategoryFlammable || a == CategoryOxidizing) {
		return true
	}
	return false
}

// unload 朴素卸货判定与执行。
func (n *naiveSystem) unload(stop int) (UnloadStatus, []int, RejectKind) {
	if stop <= n.arrivedStop {
		return UnloadAlreadyProcessed, []int{}, KindNone
	}
	minOnboard := 0
	for _, it := range n.items {
		if minOnboard == 0 || it.cargo.Stop < minOnboard {
			minOnboard = it.cargo.Stop
		}
	}
	if minOnboard > 0 && minOnboard < stop {
		return UnloadOK, nil, KindUnloadOrder
	}

	removed := make([]int, 0)
	kept := make([]naiveItem, 0, len(n.items))
	for _, it := range n.items {
		if it.cargo.Stop == stop {
			removed = append(removed, it.cargo.ID)
		} else {
			kept = append(kept, it)
		}
	}
	n.items = kept
	sortInts(removed)
	n.arrivedStop = stop
	if len(removed) == 0 {
		return UnloadEmpty, removed, KindNone
	}
	return UnloadOK, removed, KindNone
}

func (n *naiveSystem) locate(id int) (int, bool) {
	for _, it := range n.items {
		if it.cargo.ID == id {
			return it.compartment, true
		}
	}
	return 0, false
}

func (n *naiveSystem) capacities() []Capacity {
	out := make([]Capacity, len(n.compartments))
	for k := range n.compartments {
		out[k] = Capacity{Compartment: k + 1,
			RemainWeight: n.compartments[k].MaxWeight,
			RemainVolume: n.compartments[k].MaxVolume}
	}
	for _, it := range n.items {
		out[it.compartment-1].RemainWeight -= it.cargo.Weight
		out[it.compartment-1].RemainVolume -= it.cargo.Volume
	}
	return out
}
