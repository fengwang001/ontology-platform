package vanload

import "sync"

// LoadResult 单件装货成功结果。
type LoadResult struct {
	CargoID     int
	Compartment int // 确定的装载分区（1 起）
}

// UnloadStatus 卸货结果类别。
type UnloadStatus int

const (
	UnloadOK               UnloadStatus = iota // 正常卸货（卸下了货物）
	UnloadEmpty                                // 停靠点无货物，成功推进进度
	UnloadAlreadyProcessed                     // 序号不大于已到达最大序号，已处理
)

// String 返回卸货结果类别中文名。
func (u UnloadStatus) String() string {
	switch u {
	case UnloadOK:
		return "卸货"
	case UnloadEmpty:
		return "空停靠点"
	case UnloadAlreadyProcessed:
		return "已处理"
	default:
		return "未知"
	}
}

// UnloadResult 卸货成功结果。
type UnloadResult struct {
	Stop        int
	Status      UnloadStatus
	Removed     []int // 被卸下的货物编号（按编号升序）
	ArrivedStop int   // 操作完成后已到达的最大停靠点序号
}

// Capacity 分区剩余载重与剩余容积。
type Capacity struct {
	Compartment  int
	RemainWeight int
	RemainVolume int
}

// System 厢式货车配载与卸货顺序校验系统。
// 所有方法可并发调用；内部一把读写锁使结果等价于某个串行顺序。
type System struct {
	mu    sync.RWMutex
	state *state
	log   Logger
}

// New 创建系统。compartments 顺序即从车头到车尾的分区 1..N。
// 分区数必须为正，载重/容积上限必须为正整数，否则返回参数非法。
func New(compartments []Compartment, opts ...Option) (*System, error) {
	if len(compartments) == 0 {
		return nil, invalidError("至少需要一个分区")
	}
	for i, cm := range compartments {
		if cm.MaxWeight <= 0 || cm.MaxVolume <= 0 {
			return nil, invalidError("分区 " + itoa(i+1) + " 的载重与容积上限必须为正整数")
		}
	}
	sys := &System{state: newState(compartments)}
	for _, opt := range opts {
		opt(sys)
	}
	return sys, nil
}

// Load 单件装货。成功返回确定的装载分区；失败返回 *Reject 且不改变状态。
func (sys *System) Load(c Cargo) (LoadResult, error) {
	sys.mu.Lock()
	defer sys.mu.Unlock()

	if !c.Valid() {
		r := &Reject{Kind: KindInvalidArgument, FailedIndex: -1}
		sys.emit("load", cargoInput(c), rejectOutput(r), "字段必须为正整数且类别合法")
		return LoadResult{}, r
	}
	if _, exists := sys.state.items[c.ID]; exists {
		r := &Reject{Kind: KindDuplicateID, FailedIndex: -1}
		sys.emit("load", cargoInput(c), rejectOutput(r), "货物编号已在车上")
		return LoadResult{}, r
	}
	if c.Stop <= sys.state.arrivedStop {
		r := &Reject{Kind: KindStopPassed, FailedIndex: -1}
		sys.emit("load", cargoInput(c), rejectOutput(r),
			"停靠点 "+itoa(c.Stop)+" 不大于已到达最大序号 "+itoa(sys.state.arrivedStop))
		return LoadResult{}, r
	}

	f := evaluateFeasibility(sys.state, c)
	if f.chosen == 0 {
		r := &Reject{Kind: f.overall, FailedIndex: -1, CompartmentReasons: f.reasons}
		sys.emit("load", cargoInput(c), rejectOutput(r), reasonBasis(f))
		return LoadResult{}, r
	}

	sys.place(sys.state, c, f.chosen)
	res := LoadResult{CargoID: c.ID, Compartment: f.chosen}
	sys.emit("load", cargoInput(c), loadOutput(res), "满足全部约束的最小编号分区")
	return res, nil
}

// Unload 到达停靠点 stop 并卸下该停靠点全部货物。
func (sys *System) Unload(stop int) (UnloadResult, error) {
	sys.mu.Lock()
	defer sys.mu.Unlock()

	if stop <= 0 {
		r := &Reject{Kind: KindInvalidArgument, FailedIndex: -1}
		sys.emit("unload", "stop="+itoa(stop), rejectOutput(r), "停靠点序号必须为正整数")
		return UnloadResult{}, r
	}
	if stop <= sys.state.arrivedStop {
		res := UnloadResult{
			Stop:        stop,
			Status:      UnloadAlreadyProcessed,
			Removed:     []int{},
			ArrivedStop: sys.state.arrivedStop,
		}
		sys.emit("unload", "stop="+itoa(stop), unloadOutput(res), "序号不大于已到达最大序号，幂等无变化")
		return res, nil
	}

	// 顺序错误：尚有更小序号的货物在车上。
	minOnboard := 0
	for _, cs := range sys.state.states {
		if cs.count > 0 && (minOnboard == 0 || cs.minStop < minOnboard) {
			minOnboard = cs.minStop
		}
	}
	if minOnboard > 0 && minOnboard < stop {
		r := &Reject{Kind: KindUnloadOrder, FailedIndex: -1}
		sys.emit("unload", "stop="+itoa(stop), rejectOutput(r),
			"尚有停靠点 "+itoa(minOnboard)+" 的货物在车上")
		return UnloadResult{}, r
	}

	removed := sys.removeAtStop(stop)
	sys.state.arrivedStop = stop
	status := UnloadOK
	if len(removed) == 0 {
		status = UnloadEmpty
	}
	res := UnloadResult{
		Stop:        stop,
		Status:      status,
		Removed:     removed,
		ArrivedStop: stop,
	}
	sys.emit("unload", "stop="+itoa(stop), unloadOutput(res), "卸下该停靠点全部货物并推进进度")
	return res, nil
}

// RemainingCapacities 查询各分区剩余载重与容积（只读一致快照）。
func (sys *System) RemainingCapacities() []Capacity {
	sys.mu.RLock()
	defer sys.mu.RUnlock()

	out := make([]Capacity, len(sys.state.states))
	for i, cs := range sys.state.states {
		out[i] = Capacity{
			Compartment:  i + 1,
			RemainWeight: sys.state.compartments[i].MaxWeight - cs.usedWeight,
			RemainVolume: sys.state.compartments[i].MaxVolume - cs.usedVolume,
		}
	}
	return out
}

// Locate 查询某货物当前所在分区；不在车上返回 (0,false)。
func (sys *System) Locate(cargoID int) (int, bool) {
	sys.mu.RLock()
	defer sys.mu.RUnlock()
	rec, ok := sys.state.items[cargoID]
	if !ok {
		return 0, false
	}
	return rec.compartment, true
}

// ArrivedStop 查询已到达的最大停靠点序号（0 表示尚未开始）。
func (sys *System) ArrivedStop() int {
	sys.mu.RLock()
	defer sys.mu.RUnlock()
	return sys.state.arrivedStop
}

// place 把货物写入指定分区（调用方持有写锁且已判定可行）。
func (sys *System) place(s *state, c Cargo, compartment int) {
	s.states[compartment-1].addCargo(c)
	s.items[c.ID] = &itemRecord{cargo: c, compartment: compartment}
}

// removeAtStop 卸下某停靠点的全部货物，返回按编号升序的编号列表。
func (sys *System) removeAtStop(stop int) []int {
	ids := make([]int, 0)
	for id, rec := range sys.state.items {
		if rec.cargo.Stop == stop {
			ids = append(ids, id)
		}
	}
	sortInts(ids)
	for _, id := range ids {
		rec := sys.state.items[id]
		sys.state.states[rec.compartment-1].removeCargo(rec.cargo)
		delete(sys.state.items, id)
	}
	return ids
}

func (sys *System) emit(op, input, output, reason string) {
	if sys.log != nil {
		sys.log.Log(Event{Op: op, Input: input, Output: output, Reason: reason})
	}
}

func invalidError(msg string) *Reject {
	return &Reject{Kind: KindInvalidArgument, FailedIndex: -1}
}

func sortInts(a []int) {
	// 插入排序对卸货件数足够；保持零依赖并保证确定性。
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}
