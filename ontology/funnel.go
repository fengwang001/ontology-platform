// Package ontology 实现带零点击护栏与中途加入的创意连续淘汰分流器。
package ontology

import (
	"errors"
	"math/big"
	"sort"
	"sync"
)

// 各类拒绝原因，按优先级区分。
var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrFinished        = errors.New("already finished")
	ErrUnknownArm      = errors.New("arm not registered")
	ErrArmExists       = errors.New("arm already registered")
	ErrCapacity        = errors.New("arm capacity reached")
	ErrNoExposure      = errors.New("click without exposure")
)

// ElimReason 为淘汰原因。
type ElimReason string

const (
	ReasonGuardrail ElimReason = "guardrail"
	ReasonRound     ElimReason = "round"
)

// ArmSnapshot 是某个创意在某一时刻的只读状态。
type ArmSnapshot struct {
	ID            int64
	Exposure      int64
	Clicks        int64
	RoundExposure int64
	Active        bool
}

// ElimEvent 记录一次淘汰事件。
type ElimEvent struct {
	Round  int
	Arm    int64
	Reason ElimReason
}

type arm struct {
	id int64
	sT int64 // 累计曝光
	cT int64 // 累计点击
	sR int64 // 本轮曝光
	// registered 顺序隐含在 arms 切片中；active 表示当前是否活跃。
	active bool
}

// Funnel 为创意连续淘汰分流器。
//
// 所有方法均可被并发调用；每次 Next 内的曝光、护栏淘汰、收轮淘汰
// 作为一个不可分割的临界区执行，任何观察者都看不到只完成一半的 Next。
type Funnel struct {
	mu sync.Mutex

	b int64 // 基础配额
	g int64 // 护栏曝光数
	r int   // 当前轮次，从 1 起

	arms      []*arm         // 按登记顺序排列（初始创意按编号升序）
	byID      map[int64]*arm // 编号 -> 创意
	active    map[*arm]struct{}
	events    []ElimEvent
	finished  bool
	winner    int64
	nextCalls int64 // 成功的 Next 次数
}

// NewFunnel 构造分流器。
// n 为初始创意数（2..64，编号 0..n-1），b 为基础配额（1..1e6），
// G 为护栏曝光数（1..1e9）。参数非法时返回 ErrInvalidArgument。
func NewFunnel(n int, b, g int64) (*Funnel, error) {
	if n < 2 || n > 64 || b < 1 || b > 1_000_000 || g < 1 || g > 1_000_000_000 {
		return nil, ErrInvalidArgument
	}
	f := &Funnel{
		b:      b,
		g:      g,
		r:      1,
		byID:   make(map[int64]*arm),
		active: make(map[*arm]struct{}),
	}
	for id := int64(0); id < int64(n); id++ {
		a := &arm{id: id, active: true}
		f.arms = append(f.arms, a)
		f.byID[id] = a
		f.active[a] = struct{}{}
	}
	return f, nil
}

// quota 返回第 round 轮每个活跃创意的配额 q_r = b*2^min(r-1,20)。
func quota(b int64, round int) int64 {
	shift := round - 1
	if shift > 20 {
		shift = 20
	}
	return b << shift
}

// Next 分流一次曝光并返回创意编号。
//
// 在本轮 sR < q_r 的活跃创意中取 sR 最小者，同值取编号小者；
// 随后依次处理护栏淘汰与（可能触发的）收轮淘汰。结束后调用返回
// ErrFinished。
func (f *Funnel) Next() (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.finished {
		return 0, ErrFinished
	}

	q := quota(f.b, f.r)

	// 选择 sR 最小、同值编号最小的未满配额活跃创意。
	var chosen *arm
	for a := range f.active {
		if a.sR >= q {
			continue
		}
		if chosen == nil || a.sR < chosen.sR || (a.sR == chosen.sR && a.id < chosen.id) {
			chosen = a
		}
	}
	// 未结束时至少有 2 个活跃创意，不可能所有创意都满额。
	if chosen == nil {
		panic("ontology: no eligible active arm before finish")
	}

	chosen.sT++
	chosen.sR++
	f.nextCalls++
	id := chosen.id

	// （一）护栏：零点击且累计曝光达到 G，且至少有 2 个活跃创意。
	if chosen.cT == 0 && chosen.sT >= f.g && len(f.active) >= 2 {
		f.eliminate(chosen, ReasonGuardrail)
		if f.finished {
			return id, nil
		}
	}

	// （二）收轮：所有活跃创意 sR 都等于 q_r。
	f.closeRoundIfComplete(q)

	return id, nil
}

func (f *Funnel) eliminate(a *arm, reason ElimReason) {
	a.active = false
	delete(f.active, a)
	f.events = append(f.events, ElimEvent{Round: f.r, Arm: a.id, Reason: reason})
	if len(f.active) == 1 {
		for winner := range f.active {
			f.winner = winner.id
		}
		f.finished = true
	}
}

func (f *Funnel) closeRoundIfComplete(q int64) {
	if f.finished {
		return
	}
	for a := range f.active {
		if a.sR != q {
			return
		}
	}

	live := make([]*arm, 0, len(f.active))
	for a := range f.active {
		live = append(live, a)
	}
	// 累计点击率 cT/sT 降序；以 cT_i*sT_j 与 cT_j*sT_i 交叉相乘比较，
	// 相等时编号小者在前。任何已登记创意都至少有一次曝光，sT > 0。
	sort.Slice(live, func(i, j int) bool {
		x, y := live[i], live[j]
		lhs := new(big.Int).Mul(big.NewInt(x.cT), big.NewInt(y.sT))
		rhs := new(big.Int).Mul(big.NewInt(y.cT), big.NewInt(x.sT))
		if cmp := lhs.Cmp(rhs); cmp != 0 {
			return cmp > 0
		}
		return x.id < y.id
	})

	keep := (len(live) + 1) / 2 // ceil(a/2)
	for _, a := range live[keep:] {
		f.eliminate(a, ReasonRound)
	}

	if f.finished {
		return
	}

	f.r++
	for a := range f.active {
		a.sR = 0
	}
}

// Click 为创意登记一次点击。
//
// 任何已登记创意（含已淘汰者）在 cT < sT 时均可点击；已淘汰创意的
// 点击只记账，不影响任何淘汰结果。结束后仍可点击记账。
func (f *Funnel) Click(id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	a, ok := f.byID[id]
	if !ok {
		return ErrUnknownArm
	}
	if a.cT >= a.sT {
		return ErrNoExposure
	}
	a.cT++
	return nil
}

// Join 使一个新创意以活跃状态加入当前轮。
//
// 新创意 sT、cT、sR 均为 0，须补满本轮配额，因此会被优先分流。
// id 必须在 0..1000 内、从未登记过，且创意总数少于 64；结束后不可加入。
func (f *Funnel) Join(id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if id < 0 || id > 1000 {
		return ErrInvalidArgument
	}
	if f.finished {
		return ErrFinished
	}
	if _, ok := f.byID[id]; ok {
		return ErrArmExists
	}
	if len(f.arms) >= 64 {
		return ErrCapacity
	}
	a := &arm{id: id, active: true}
	f.arms = append(f.arms, a)
	f.byID[id] = a
	f.active[a] = struct{}{}
	return nil
}

// Round 返回当前轮次（结束后为结束时的轮次）。
func (f *Funnel) Round() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.r
}

// Finished 报告分流是否已结束。
func (f *Funnel) Finished() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.finished
}

// Winner 返回胜出创意编号；未结束时第二个返回值为 false。
func (f *Funnel) Winner() (int64, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.winner, f.finished
}

// ActiveCount 返回当前活跃创意数。
func (f *Funnel) ActiveCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.active)
}

// TotalExposures 返回成功 Next 的总次数（等于所有创意累计曝光之和）。
func (f *Funnel) TotalExposures() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.nextCalls
}

// Snapshot 返回所有已登记创意按登记顺序排列的只读快照。
func (f *Funnel) Snapshot() []ArmSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]ArmSnapshot, len(f.arms))
	for i, a := range f.arms {
		out[i] = ArmSnapshot{
			ID:            a.id,
			Exposure:      a.sT,
			Clicks:        a.cT,
			RoundExposure: a.sR,
			Active:        a.active,
		}
	}
	return out
}

// EliminationEvents 返回截至目前按发生顺序排列的淘汰事件副本。
func (f *Funnel) EliminationEvents() []ElimEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]ElimEvent, len(f.events))
	copy(out, f.events)
	return out
}
