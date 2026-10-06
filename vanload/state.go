package vanload

// compartmentState 单个分区的聚合状态。
// 只保存总量、各类别件数与停靠点序号分布，
// 因此单件货物的放置判定只需扫描分区、无需枚举在车货物。
type compartmentState struct {
	usedWeight int // 已用载重（克）
	usedVolume int // 已用容积（立方厘米）

	// 各类别货物件数：存在性由“计数 > 0”判定，卸货后可精确维护。
	foodCount      int
	flammableCount int
	oxidizingCount int

	// 各停靠点序号在本分区内的件数，用于卸货后维护序号极值。
	stopCount map[int]int
	minStop   int // 区内货物最小停靠点序号；空分区为 0
	maxStop   int // 区内货物最大停靠点序号；空分区为 0
	count     int // 区内货物件数
}

func newCompartmentState() *compartmentState {
	return &compartmentState{stopCount: make(map[int]int)}
}

// addCargo 向分区聚合状态加入一件货物（调用方已保证约束成立）。
func (cs *compartmentState) addCargo(c Cargo) {
	cs.usedWeight += c.Weight
	cs.usedVolume += c.Volume
	switch c.Category {
	case CategoryFood:
		cs.foodCount++
	case CategoryFlammable:
		cs.flammableCount++
	case CategoryOxidizing:
		cs.oxidizingCount++
	}
	if cs.count == 0 {
		cs.minStop = c.Stop
		cs.maxStop = c.Stop
	} else {
		if c.Stop < cs.minStop {
			cs.minStop = c.Stop
		}
		if c.Stop > cs.maxStop {
			cs.maxStop = c.Stop
		}
	}
	cs.stopCount[c.Stop]++
	cs.count++
}

// removeCargo 从分区聚合状态移除一件货物。
func (cs *compartmentState) removeCargo(c Cargo) {
	cs.usedWeight -= c.Weight
	cs.usedVolume -= c.Volume
	switch c.Category {
	case CategoryFood:
		cs.foodCount--
	case CategoryFlammable:
		cs.flammableCount--
	case CategoryOxidizing:
		cs.oxidizingCount--
	}
	cs.stopCount[c.Stop]--
	if cs.stopCount[c.Stop] == 0 {
		delete(cs.stopCount, c.Stop)
		if c.Stop == cs.minStop || c.Stop == cs.maxStop {
			cs.recomputeStopRange()
		}
	}
	cs.count--
	if cs.count == 0 {
		cs.minStop = 0
		cs.maxStop = 0
	}
}

// recomputeStopRange 重新计算区内停靠点序号范围。
// 仅在某个极值停靠点的最后一件被卸走时触发。
func (cs *compartmentState) recomputeStopRange() {
	if len(cs.stopCount) == 0 {
		cs.minStop = 0
		cs.maxStop = 0
		return
	}
	first := true
	for stop := range cs.stopCount {
		if first {
			cs.minStop, cs.maxStop = stop, stop
			first = false
			continue
		}
		if stop < cs.minStop {
			cs.minStop = stop
		}
		if stop > cs.maxStop {
			cs.maxStop = stop
		}
	}
}

// itemRecord 一件在车货物的记录。
type itemRecord struct {
	cargo       Cargo
	compartment int // 所在分区编号（1 起）
}

// state 系统的全部可变状态。修改必须在互斥保护下（或对副本修改后整体提交）。
type state struct {
	compartments []Compartment       // 下标 0 对应分区 1
	states       []*compartmentState // 与 compartments 等长
	items        map[int]*itemRecord // 货物编号 -> 记录
	arrivedStop  int                 // 已到达的最大停靠点序号；0 表示尚未开始卸货
}

func newState(cmps []Compartment) *state {
	st := &state{
		compartments: make([]Compartment, len(cmps)),
		states:       make([]*compartmentState, len(cmps)),
		items:        make(map[int]*itemRecord),
	}
	copy(st.compartments, cmps)
	for i := range st.states {
		st.states[i] = newCompartmentState()
	}
	return st
}

// clone 深拷贝状态，供批量装货“试装—提交”使用。
func (s *state) clone() *state {
	c := newState(s.compartments)
	for id, rec := range s.items {
		c.states[rec.compartment-1].addCargo(rec.cargo)
		c.items[id] = &itemRecord{cargo: rec.cargo, compartment: rec.compartment}
	}
	c.arrivedStop = s.arrivedStop
	return c
}

// placedItems 返回当前在车货物编号到分区的映射快照（供测试/对照使用）。
func (s *state) placedItems() map[int]int {
	out := make(map[int]int, len(s.items))
	for id, rec := range s.items {
		out[id] = rec.compartment
	}
	return out
}
