// Package eliminator 实现带零点击护栏与中途加入的创意连续淘汰分流器。
//
// 所有方法均为并发安全：内部用单一互斥锁串行化，护栏淘汰与收轮是 Next
// 不可分割的一部分，任何观察者都不会看到只完成一半的 Next。
package eliminator

import (
	"sort"
	"sync"
)

// RejectReason 描述被拒绝操作的原因。
type RejectReason string

const (
	ReasonInvalidArgs      RejectReason = "invalid_arguments"      // 参数非法
	ReasonFinished         RejectReason = "already_finished"       // 已结束
	ReasonArmNotFound      RejectReason = "arm_not_found"          // 创意不存在
	ReasonArmAlreadyExists RejectReason = "arm_already_exists"     // 创意已存在
	ReasonFull             RejectReason = "capacity_full"          // 创意已满
	ReasonClickNoExposure  RejectReason = "click_without_exposure" // 点击无对应曝光
)

const (
	maxArmID    = 1000
	maxArmCount = 64
	capRound    = 20 // 翻倍指数在 min(r-1,20) 处封顶
)

// RejectError 携带可区分的拒绝原因。被拒绝的操作不改变任何状态。
type RejectError struct {
	Reason RejectReason
}

func (e *RejectError) Error() string { return string(e.Reason) }

func reject(r RejectReason) error { return &RejectError{Reason: r} }

// Elimination 记录一次淘汰事件。
type Elimination struct {
	Arm    int
	Round  int
	Reason string // "guard"（护栏）或 "round"（收轮）
}

// NextResult 是一次成功 Next 的完整可观察结果。
type NextResult struct {
	Arm         int
	Eliminated  []Elimination
	RoundClosed bool
	Winner      int // -1 表示尚无胜出者
	Round       int // 结果产生时（收轮前）所处的轮次
}

// ArmState 是单个创意的状态快照。
type ArmState struct {
	ID         int
	Exposures  int64
	Clicks     int64
	RoundExp   int64
	Eliminated bool
	ElimReason string
}

// Snapshot 是分流器某一时刻的完整状态。
type Snapshot struct {
	Round        int
	Finished     bool
	Winner       int
	TotalNext    int64
	Arms         []ArmState
	Eliminations []Elimination
}

type arm struct {
	id         int
	sT         int64 // 累计曝光
	cT         int64 // 累计点击
	sR         int64 // 本轮曝光
	eliminated bool
	elimReason string
}

// Eliminator 是并发安全的创意连续淘汰分流器。
type Eliminator struct {
	mu     sync.Mutex
	base   int64
	guard  int64
	round  int
	arms   map[int]*arm
	order  []int // 登记顺序，用于快照稳定输出
	active []int
	total  int64 // 成功 Next 次数
	done   bool
	winner int
	events []Elimination
}

// New 创建分流器。
// n 为初始创意数（2..64，编号 0..n-1），b 为基础配额（1..1e6），
// G 为护栏曝光数（1..1e9）。
func New(n int, b, G int64) (*Eliminator, error) {
	if n < 2 || n > maxArmCount || b < 1 || b > 1_000_000 || G < 1 || G > 1_000_000_000 {
		return nil, reject(ReasonInvalidArgs)
	}
	e := &Eliminator{
		base:   b,
		guard:  G,
		round:  1,
		arms:   make(map[int]*arm),
		winner: -1,
	}
	for id := 0; id < n; id++ {
		e.arms[id] = &arm{id: id}
		e.order = append(e.order, id)
		e.active = append(e.active, id)
	}
	return e, nil
}

// quota 返回当前轮每个活跃创意的配额 q_r = b * 2^min(r-1,20)。
func (e *Eliminator) quota() int64 {
	k := int64(e.round - 1)
	if k > capRound {
		k = capRound
	}
	return e.base * (int64(1) << k)
}

// rateGreaterThan 按累计点击率比较：cT_x/sT_x > cT_y/sT_y，
// 用整数交叉相乘 cT_x*sT_y vs cT_y*sT_x；相等时编号小者在前。
// 走到收轮时活跃创意必有 sT > 0（每次收轮前都刚完成一次曝光）。
func (x *arm) rateGreaterThan(y *arm) bool {
	l := x.cT * y.sT
	r := y.cT * x.sT
	if l != r {
		return l > r
	}
	return x.id < y.id
}

// Next 分配下一次曝光。
// 选择规则：在本轮 sR < q_r 的活跃创意中取 sR 最小者，同值取编号最小者。
// 选中后其 sT 与 sR 各加一，随后依次处理护栏与收轮。
func (e *Eliminator) Next() (NextResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.done {
		return NextResult{}, reject(ReasonFinished)
	}

	q := e.quota()
	pick := -1
	var bestSR int64
	for _, id := range e.active {
		a := e.arms[id]
		if a.sR >= q {
			continue
		}
		if pick == -1 || a.sR < bestSR || (a.sR == bestSR && a.id < pick) {
			pick = a.id
			bestSR = a.sR
		}
	}
	// 未结束时至少有两个活跃创意；每次收轮后活跃创意 sR 都清零，
	// 因此未收轮时至少有一个创意 sR < q，这里必然选得到。
	a := e.arms[pick]
	a.sT++
	a.sR++
	e.total++

	res := NextResult{Arm: pick, Winner: -1, Round: e.round}

	// （一）零点击护栏：cT=0 且 sT>=G，且活跃创意不少于 2 时立即淘汰。
	if a.cT == 0 && a.sT >= e.guard && len(e.active) >= 2 {
		a.eliminated = true
		a.elimReason = "guard"
		ev := Elimination{Arm: a.id, Round: e.round, Reason: "guard"}
		e.events = append(e.events, ev)
		res.Eliminated = append(res.Eliminated, ev)
		e.removeActive(a.id)
		if len(e.active) == 1 {
			e.finish(e.active[0])
			res.Winner = e.winner
			return res, nil
		}
	}

	// （二）收轮：所有活跃创意本轮 sR 都等于 q_r。
	closed := true
	for _, id := range e.active {
		if e.arms[id].sR < q {
			closed = false
			break
		}
	}
	if closed {
		e.closeRound(&res)
	}
	return res, nil
}

func (e *Eliminator) removeActive(id int) {
	for i, v := range e.active {
		if v == id {
			e.active = append(e.active[:i], e.active[i+1:]...)
			return
		}
	}
}

// closeRound 执行收轮淘汰，必须在持锁状态下调用。
func (e *Eliminator) closeRound(res *NextResult) {
	ranked := make([]*arm, len(e.active))
	for i, id := range e.active {
		ranked[i] = e.arms[id]
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		return ranked[i].rateGreaterThan(ranked[j])
	})

	keep := (len(ranked) + 1) / 2 // ceil(a/2)

	dropIDs := make([]int, 0, len(ranked)-keep)
	for _, a := range ranked[keep:] {
		dropIDs = append(dropIDs, a.id)
	}
	sort.Ints(dropIDs) // 淘汰事件按编号确定序输出，保证可复现

	for _, id := range dropIDs {
		a := e.arms[id]
		a.eliminated = true
		a.elimReason = "round"
		ev := Elimination{Arm: id, Round: e.round, Reason: "round"}
		e.events = append(e.events, ev)
		res.Eliminated = append(res.Eliminated, ev)
	}

	survivors := append([]int(nil), rankedToIDs(ranked[:keep])...)
	res.RoundClosed = true

	if len(survivors) == 1 {
		e.active = survivors
		e.finish(survivors[0])
		res.Winner = e.winner
		return
	}

	e.round++
	e.active = survivors
	for _, id := range e.active {
		e.arms[id].sR = 0
	}
}

func rankedToIDs(ranked []*arm) []int {
	ids := make([]int, len(ranked))
	for i, a := range ranked {
		ids[i] = a.id
	}
	return ids
}

// finish 锁定胜出者并结束，必须在持锁状态下调用。
func (e *Eliminator) finish(id int) {
	e.done = true
	e.winner = id
}

// Click 为指定创意记一次点击。
// 对任何已登记创意（含已淘汰者、结束之后），cT < sT 才成功。
// 已淘汰创意的点击只记账，不影响任何淘汰结果。
func (e *Eliminator) Click(arm int) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	a, ok := e.arms[arm]
	if !ok {
		return reject(ReasonArmNotFound)
	}
	if a.cT >= a.sT {
		return reject(ReasonClickNoExposure)
	}
	a.cT++
	return nil
}

// Join 在当前轮中途注册一个新创意。
// id 为 0..1000 中尚未登记过的编号，总数最多 64；新创意以活跃状态
// 加入当前轮（sT=cT=sR=0），须补满本轮配额，因此会被优先分流。
// 拒绝优先级：参数非法 > 已结束 > 编号已登记（含已淘汰者）> 总数已满。
func (e *Eliminator) Join(id int) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if id < 0 || id > maxArmID {
		return reject(ReasonInvalidArgs)
	}
	if e.done {
		return reject(ReasonFinished)
	}
	if _, ok := e.arms[id]; ok {
		return reject(ReasonArmAlreadyExists)
	}
	if len(e.arms) >= maxArmCount {
		return reject(ReasonFull)
	}
	e.arms[id] = &arm{id: id}
	e.order = append(e.order, id)
	// 新创意 sR=0 而老创意 sR 通常已大于 0，Next 会立刻优先分流它。
	e.active = append(e.active, id)
	return nil
}

// Snapshot 返回当前状态的不可变拷贝。
func (e *Eliminator) Snapshot() Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()

	snap := Snapshot{
		Round:     e.round,
		Finished:  e.done,
		Winner:    e.winner,
		TotalNext: e.total,
	}
	for _, id := range e.order {
		a := e.arms[id]
		snap.Arms = append(snap.Arms, ArmState{
			ID:         a.id,
			Exposures:  a.sT,
			Clicks:     a.cT,
			RoundExp:   a.sR,
			Eliminated: a.eliminated,
			ElimReason: a.elimReason,
		})
	}
	snap.Eliminations = append(snap.Eliminations, e.events...)
	return snap
}
