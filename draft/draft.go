// Package draft 实现对战开局前两阶段禁选的轮次控制器：
// 步时钟、按队共享的备用时间、超时自动处理与手动操作判定。
package draft

import (
	"errors"
	"fmt"
	"sync"

	"ontology/order"
	"ontology/pool"
)

// 操作被拒绝的原因，判定优先级按声明顺序。
var (
	ErrParam           = errors.New("draft: 参数非法")
	ErrClock           = errors.New("draft: 时钟回退")
	ErrDone            = errors.New("draft: 已全部结束")
	ErrStepType        = errors.New("draft: 当前步类型不符")
	ErrNotActingTeam   = errors.New("draft: 该玩家不属于行动队")
	ErrAlreadyPicked   = errors.New("draft: 该玩家已选过")
	ErrHeroUnavailable = errors.New("draft: 英雄不可用")
)

const maxNow = int64(1_000_000_000_000)

// Snapshot 是 State 返回的只读快照。
type Snapshot struct {
	Step  int      // 当前步序号（0 起）；结束时等于总步数
	Team  int      // 当前步行动队（1 或 2）；结束时为 0
	Bank  [2]int64 // 两队剩余备用时间（毫秒）
	Picks []int    // 各玩家所选英雄，0 表示未选
	Bans  []int    // 已被禁英雄（空禁不记录），按禁用顺序
	Done  bool     // 是否已全部结束
}

// Draft 是禁选轮次控制器，所有方法可并发调用，效果等价于某个串行顺序。
type Draft struct {
	mu     sync.Mutex
	n      int
	t      int64
	h      int
	steps  []order.Step
	pool   *pool.Pool
	idx    int      // 当前步序号
	start  int64    // 当前步起始时刻
	bank   [2]int64 // 两队剩余备用时间
	picks  []int    // 各玩家所选英雄，0 表示未选
	bans   []int    // 已被禁英雄
	hover  []int    // 各玩家预选英雄，0 表示无预选
	maxNow int64    // 已接受操作的最大 now（初始为 now0）
}

// New 创建控制器。参数越界时 panic：
// 1<=n<=5；0<=b1,b2<=5；0<=c<=2n 且 c==2n 时 b2==0；
// 1<=T<=1e6；0<=Bk<=1e6；2n+2(b1+b2)<=H<=1e5；0<=now0<=1e12。
func New(n, b1, c, b2 int, T, Bk int64, H int, now0 int64) *Draft {
	if n < 1 || n > 5 {
		panic(fmt.Sprintf("draft.New: n=%d 越界", n))
	}
	if b1 < 0 || b1 > 5 || b2 < 0 || b2 > 5 {
		panic(fmt.Sprintf("draft.New: b1=%d b2=%d 越界", b1, b2))
	}
	if c < 0 || c > 2*n || (c == 2*n && b2 != 0) {
		panic(fmt.Sprintf("draft.New: c=%d 与 b2=%d 矛盾", c, b2))
	}
	if T < 1 || T > 1_000_000 {
		panic(fmt.Sprintf("draft.New: T=%d 越界", T))
	}
	if Bk < 0 || Bk > 1_000_000 {
		panic(fmt.Sprintf("draft.New: Bk=%d 越界", Bk))
	}
	if H < 2*n+2*(b1+b2) || H > 100_000 {
		panic(fmt.Sprintf("draft.New: H=%d 越界", H))
	}
	if now0 < 0 || now0 > maxNow {
		panic(fmt.Sprintf("draft.New: now0=%d 越界", now0))
	}
	return &Draft{
		n:      n,
		t:      T,
		h:      H,
		steps:  order.Steps(n, b1, c, b2),
		pool:   pool.New(H),
		start:  now0,
		bank:   [2]int64{Bk, Bk},
		picks:  make([]int, 2*n),
		hover:  make([]int, 2*n),
		maxNow: now0,
	}
}

// teamOf 返回玩家所属队：0..n-1 为队 1，n..2n-1 为队 2。
func (d *Draft) teamOf(player int) int {
	if player < d.n {
		return 1
	}
	return 2
}

// checkClock 校验 now 的范围与单调性。
func (d *Draft) checkClock(now int64) error {
	if now < 0 || now > maxNow {
		return ErrParam
	}
	if now < d.maxNow {
		return ErrClock
	}
	return nil
}

// process 入口处理：把所有超时时刻 X<=now 的步依次自动处理，
// 超时队备用时间归零，下一步起始于其逻辑超时时刻 X 而非 now。
func (d *Draft) process(now int64) {
	for d.idx < len(d.steps) {
		st := d.steps[d.idx]
		x := d.start + d.t + d.bank[st.Team-1]
		if x > now {
			return
		}
		d.bank[st.Team-1] = 0
		if st.Kind == order.Pick {
			player := d.firstUnpicked(st.Team)
			hero := d.hover[player]
			if hero == 0 || !d.pool.Available(hero) {
				hero = d.pool.MinAvailable()
			}
			d.pool.Pick(hero)
			d.picks[player] = hero
		}
		// 禁用步超时为空禁：不消耗任何英雄。
		d.start = x
		d.idx++
	}
}

// firstUnpicked 返回行动队中编号最小的未选玩家。
func (d *Draft) firstUnpicked(team int) int {
	lo, hi := 0, d.n
	if team == 2 {
		lo, hi = d.n, 2*d.n
	}
	for p := lo; p < hi; p++ {
		if d.picks[p] == 0 {
			return p
		}
	}
	return -1 // 不可达：每队恰有 n 手选人
}

// act 应用一次手动行动：扣备用、推进步时钟。
func (d *Draft) act(team int, now int64) {
	if over := now - d.start - d.t; over > 0 {
		d.bank[team-1] -= over
	}
	d.start = now
	d.idx++
	d.maxNow = now
}

func (d *Draft) done() bool { return d.idx == len(d.steps) }

// Ban 在当前禁用步由 player 禁用 hero。
func (d *Draft) Ban(now int64, player, hero int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if player < 0 || player >= 2*d.n || hero < 1 || hero > d.h {
		return ErrParam
	}
	if err := d.checkClock(now); err != nil {
		return err
	}
	if now < d.start { // 见 DESIGN.md：守住步起始时刻非降
		return ErrClock
	}
	d.process(now)
	if d.done() {
		return ErrDone
	}
	st := d.steps[d.idx]
	if st.Kind != order.Ban {
		return ErrStepType
	}
	if d.teamOf(player) != st.Team {
		return ErrNotActingTeam
	}
	if !d.pool.Available(hero) {
		return ErrHeroUnavailable
	}
	d.pool.Ban(hero)
	d.bans = append(d.bans, hero)
	d.act(st.Team, now)
	return nil
}

// Pick 在当前选人步由 player 选取 hero。
func (d *Draft) Pick(now int64, player, hero int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if player < 0 || player >= 2*d.n || hero < 1 || hero > d.h {
		return ErrParam
	}
	if err := d.checkClock(now); err != nil {
		return err
	}
	if now < d.start {
		return ErrClock
	}
	d.process(now)
	if d.done() {
		return ErrDone
	}
	st := d.steps[d.idx]
	if st.Kind != order.Pick {
		return ErrStepType
	}
	if d.teamOf(player) != st.Team {
		return ErrNotActingTeam
	}
	if d.picks[player] != 0 {
		return ErrAlreadyPicked
	}
	if !d.pool.Available(hero) {
		return ErrHeroUnavailable
	}
	d.pool.Pick(hero)
	d.picks[player] = hero
	d.act(st.Team, now)
	return nil
}

// Hover 设置 player 的预选英雄，不校验英雄是否可用。
func (d *Draft) Hover(now int64, player, hero int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if player < 0 || player >= 2*d.n || hero < 1 || hero > d.h {
		return ErrParam
	}
	if err := d.checkClock(now); err != nil {
		return err
	}
	d.process(now)
	if d.done() {
		return ErrDone
	}
	if d.picks[player] != 0 {
		return ErrAlreadyPicked
	}
	d.hover[player] = hero
	d.maxNow = now
	return nil
}

// Advance 只做入口处理：自动处理所有超时时刻不大于 now 的步。
func (d *Draft) Advance(now int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.checkClock(now); err != nil {
		return err
	}
	d.process(now)
	d.maxNow = now
	return nil
}

// State 先做入口处理，再返回当前状态快照。
func (d *Draft) State(now int64) (Snapshot, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.checkClock(now); err != nil {
		return Snapshot{}, err
	}
	d.process(now)
	d.maxNow = now
	snap := Snapshot{
		Step:  d.idx,
		Bank:  d.bank,
		Picks: append([]int(nil), d.picks...),
		Bans:  append([]int(nil), d.bans...),
		Done:  d.done(),
	}
	if !snap.Done {
		snap.Team = d.steps[d.idx].Team
	}
	return snap, nil
}
