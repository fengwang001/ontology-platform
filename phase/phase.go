// Package phase 实现单轮的两阶段状态机：到达阶段收集 Arrive，
// 集满 N 个后释放并转入离开阶段；离开阶段收集 Depart，集满 N 个本轮结束。
// 本包不做合法性判定（那是 bar 的职责），只维护集合与阶段位。
package phase

// Stage 表示单轮当前所处的阶段。
type Stage int

const (
	// Arriving 到达阶段：仍在收集 Arrive。
	Arriving Stage = iota
	// Departing 离开阶段：已释放，正在收集 Depart。
	Departing
)

// Round 是单轮状态：已到达集、已离开集与阶段位。
type Round struct {
	n        int
	arrived  map[string]struct{}
	departed map[string]struct{}
	stage    Stage
}

// New 创建一轮，n 为进程总数（必须 > 0）。
func New(n int) *Round {
	if n <= 0 {
		panic("phase: n must be positive")
	}
	return &Round{
		n:        n,
		arrived:  make(map[string]struct{}, n),
		departed: make(map[string]struct{}, n),
		stage:    Arriving,
	}
}

// Stage 返回当前阶段。
func (r *Round) Stage() Stage { return r.stage }

// Released 报告本轮是否已释放（不变量 1：当且仅当已到达集曾集满 N，
// 即阶段已推进到 Departing；释放是闩锁状态，离开阶段内保持为真）。
func (r *Round) Released() bool { return r.stage == Departing }

// HasArrived 报告 id 是否仍在已到达集中。
func (r *Round) HasArrived(id string) bool {
	_, ok := r.arrived[id]
	return ok
}

// HasDeparted 报告 id 是否已在已离开集中。
func (r *Round) HasDeparted(id string) bool {
	_, ok := r.departed[id]
	return ok
}

// AddArrival 把 id 记入已到达集；若因此集满 N，转入离开阶段。
// 返回本次调用是否触发了释放。调用方须先完成合法性校验。
func (r *Round) AddArrival(id string) (released bool) {
	r.arrived[id] = struct{}{}
	if r.stage == Arriving && len(r.arrived) == r.n {
		r.stage = Departing
		return true
	}
	return false
}

// MoveToDeparted 把 id 从已到达集移入已离开集；
// 若已离开集因此集满 N，返回 true 表示本轮结束（调用方负责推进轮次）。
func (r *Round) MoveToDeparted(id string) (roundOver bool) {
	delete(r.arrived, id)
	r.departed[id] = struct{}{}
	return len(r.departed) == r.n
}
