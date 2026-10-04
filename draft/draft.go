// Package draft 实现对战开局前两阶段禁用/选人的轮次控制器：
// 步时钟、每队共享备用时间、超时自动空禁/自动选人与确定性入口处理。
package draft

import (
	"errors"
	"sync"

	"ontology/order"
	"ontology/pool"
)

// 参数与状态机各阶段的拒绝原因；按题给拒绝次序定义。
var (
	ErrInvalid     = errors.New("draft: invalid argument")
	ErrClockRewind = errors.New("draft: clock rewind")
	ErrFinished    = errors.New("draft: all steps finished")
	ErrWrongKind   = errors.New("draft: current step kind mismatch")
	ErrWrongTeam   = errors.New("draft: player not on acting team")
	ErrAlreadyPick = errors.New("draft: player already picked")
	ErrHeroBusy    = errors.New("draft: hero banned or picked")
)

// State 是某一时刻的控制器快照。
type State struct {
	Step      int      // 当前步序号（从 0 起）；全部结束时为总步数
	TotalStep int      // 总步数
	Team      int      // 当前步行动队；全部结束时为 0
	KindIsBan bool     // 当前步是否为禁用步
	Start     int64    // 当前步逻辑起始时刻
	Deadline  int64    // 当前步超时时刻 X；全部结束时为 0
	Reserve   [3]int64 // 两队剩余备用时间，下标 1、2
	Picks     []int    // 长度 2n，-1 表示未选，否则为所选英雄
	Bans      [2][]int // 两队手动禁用列表（空禁不留记录）
	Hover     []int    // 长度 2n，-1 表示无预选
}

// Draft 是轮次控制器；所有方法可并发调用。
type Draft struct {
	mu sync.Mutex

	n     int
	b1    int
	c     int
	b2    int
	t     int64
	h     int
	now0  int64
	steps []order.Step

	pool    *pool.Pool
	reserve [3]int64
	step    int
	start   int64
	picks   []int
	bans    [2][]int
	hover   []int
	lastNow int64

	removed []int // 本次入口试探性追赶期间占用的英雄，供被拒后回滚
}

// New 创建控制器。参数范围见包级说明，越界返回 ErrInvalid（包装原因仍可用 errors.Is 判定）。
func New(n, b1, c, b2 int, t, bk int64, h int, now0 int64) (*Draft, error) {
	if n < 1 || n > 5 {
		return nil, ErrInvalid
	}
	if b1 < 0 || b1 > 5 || b2 < 0 || b2 > 5 {
		return nil, ErrInvalid
	}
	if c < 0 || c > 2*n || (c == 2*n && b2 != 0) {
		return nil, ErrInvalid
	}
	if t < 1 || t > 1_000_000 {
		return nil, ErrInvalid
	}
	if bk < 0 || bk > 1_000_000 {
		return nil, ErrInvalid
	}
	if h < 2*n+2*(b1+b2) || h > 100_000 {
		return nil, ErrInvalid
	}
	if now0 < 0 || now0 > 1_000_000_000_000 {
		return nil, ErrInvalid
	}

	d := &Draft{
		n:       n,
		b1:      b1,
		c:       c,
		b2:      b2,
		t:       t,
		h:       h,
		now0:    now0,
		steps:   order.Build(order.Config{N: n, B1: b1, C: c, B2: b2}),
		pool:    pool.New(h),
		step:    0,
		start:   now0,
		picks:   make([]int, 2*n),
		hover:   make([]int, 2*n),
		lastNow: now0,
	}
	for i := range d.picks {
		d.picks[i] = -1
		d.hover[i] = -1
	}
	d.reserve[1] = bk
	d.reserve[2] = bk
	return d, nil
}

// Ban 为指定玩家在 now 手动禁用 hero。
func (d *Draft) Ban(now int64, player, hero int) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !validNow(now) || !validPlayer(player, 2*d.n) || !validHero(hero, d.h) {
		return ErrInvalid
	}
	if now < d.now0 || now < d.lastNow {
		return ErrClockRewind
	}

	cp := d.checkpoint()
	d.catchup(now)

	var err error
	switch {
	case d.step >= len(d.steps):
		err = ErrFinished
	case d.steps[d.step].Kind != order.KindBan:
		err = ErrWrongKind
	case !d.onActingTeam(player):
		err = ErrWrongTeam
	case !d.pool.Available(hero):
		err = ErrHeroBusy
	}
	if err != nil {
		d.rollback(cp)
		return err
	}

	d.manualAdvance(now, hero)
	d.lastNow = now
	return nil
}

// Pick 为指定玩家在 now 手动选择 hero。
func (d *Draft) Pick(now int64, player, hero int) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !validNow(now) || !validPlayer(player, 2*d.n) || !validHero(hero, d.h) {
		return ErrInvalid
	}
	if now < d.now0 || now < d.lastNow {
		return ErrClockRewind
	}

	cp := d.checkpoint()
	d.catchup(now)

	var err error
	switch {
	case d.step >= len(d.steps):
		err = ErrFinished
	case d.steps[d.step].Kind != order.KindPick:
		err = ErrWrongKind
	case !d.onActingTeam(player):
		err = ErrWrongTeam
	case d.picks[player] != -1:
		err = ErrAlreadyPick
	case !d.pool.Available(hero):
		err = ErrHeroBusy
	}
	if err != nil {
		d.rollback(cp)
		return err
	}

	d.manualAdvance(now, hero)
	d.picks[player] = hero
	d.lastNow = now
	return nil
}

// Hover 设置 player 的预选英雄（不校验英雄是否可用）。
func (d *Draft) Hover(now int64, player, hero int) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !validNow(now) || !validPlayer(player, 2*d.n) || !validHero(hero, d.h) {
		return ErrInvalid
	}
	if now < d.now0 || now < d.lastNow {
		return ErrClockRewind
	}

	cp := d.checkpoint()
	d.catchup(now)

	var err error
	switch {
	case d.step >= len(d.steps):
		err = ErrFinished
	case d.picks[player] != -1:
		err = ErrAlreadyPick
	}
	if err != nil {
		d.rollback(cp)
		return err
	}

	d.hover[player] = hero
	d.lastNow = now
	return nil
}

// Advance 只做入口处理：把 now 之前（含等于时刻）应超时的步自动处理完。
func (d *Draft) Advance(now int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !validNow(now) {
		return ErrInvalid
	}
	if now < d.now0 || now < d.lastNow {
		return ErrClockRewind
	}

	d.removed = d.removed[:0]
	d.catchup(now)
	d.lastNow = now
	return nil
}

// State 先做入口处理再返回当前快照。
func (d *Draft) State(now int64) (*State, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !validNow(now) {
		return nil, ErrInvalid
	}
	if now < d.now0 || now < d.lastNow {
		return nil, ErrClockRewind
	}

	d.removed = d.removed[:0]
	d.catchup(now)
	d.lastNow = now

	st := &State{
		Step:      d.step,
		TotalStep: len(d.steps),
		Start:     d.start,
		Reserve:   d.reserve,
		Picks:     append([]int(nil), d.picks...),
		Hover:     append([]int(nil), d.hover...),
		Bans:      [2][]int{append([]int(nil), d.bans[0]...), append([]int(nil), d.bans[1]...)},
	}
	if d.step < len(d.steps) {
		st.Team = d.steps[d.step].Team
		st.KindIsBan = d.steps[d.step].Kind == order.KindBan
		st.Deadline = d.start + d.t + d.reserve[st.Team]
	}
	return st, nil
}

func validNow(now int64) bool {
	return now >= 0 && now <= 1_000_000_000_000
}

func validPlayer(player, total int) bool {
	return player >= 0 && player < total
}

func validHero(hero, h int) bool {
	return hero >= 1 && hero <= h
}

func (d *Draft) onActingTeam(player int) bool {
	team := d.steps[d.step].Team
	lo := (team - 1) * d.n
	return player >= lo && player < lo+d.n
}

// deadline 返回当前步的逻辑超时时刻 X=s+T+r。
func (d *Draft) deadline() int64 {
	team := d.steps[d.step].Team
	return d.start + d.t + d.reserve[team]
}

// catchup 依次自动处理所有超时时刻不大于 now 的步；其写入可能被 rollback 撤销。
func (d *Draft) catchup(now int64) {
	for d.step < len(d.steps) {
		x := d.deadline()
		if x > now {
			return
		}
		team := d.steps[d.step].Team
		d.reserve[team] = 0
		if d.steps[d.step].Kind == order.KindBan {
			// 空禁：不禁任何英雄，不留禁用记录。
		} else {
			d.autoPick(team)
		}
		d.step++
		d.start = x
	}
}

// autoPick 由行动队编号最小的未选玩家行动：预选仍可用则用预选，否则选最小可用英雄。
func (d *Draft) autoPick(team int) {
	lo := (team - 1) * d.n
	player := lo
	for player < lo+d.n && d.picks[player] != -1 {
		player++
	}
	hero := d.hover[player]
	if hero == -1 || !d.pool.Available(hero) {
		h, ok := d.pool.FirstAvailable()
		if !ok {
			return
		}
		hero = h
	}
	d.pool.Remove(hero)
	d.removed = append(d.removed, hero)
	d.picks[player] = hero
}

// manualAdvance 落定一步有效的手动操作（Ban 或 Pick），扣备用并推进起点。
func (d *Draft) manualAdvance(now int64, hero int) {
	team := d.steps[d.step].Team
	if elapsed := now - d.start - d.t; elapsed > 0 {
		d.reserve[team] -= elapsed
	}
	d.pool.Remove(hero)
	if d.steps[d.step].Kind == order.KindBan {
		d.bans[team-1] = append(d.bans[team-1], hero)
	}
	d.removed = append(d.removed, hero)
	d.step++
	d.start = now
}

// checkpoint 保存入口追赶前的全部可观察状态。
type checkpoint struct {
	reserve [3]int64
	step    int
	start   int64
	picks   []int
	bans    [2][]int
	hover   []int
	removed []int
}

func (d *Draft) checkpoint() checkpoint {
	return checkpoint{
		reserve: d.reserve,
		step:    d.step,
		start:   d.start,
		picks:   append([]int(nil), d.picks...),
		bans:    [2][]int{append([]int(nil), d.bans[0]...), append([]int(nil), d.bans[1]...)},
		hover:   append([]int(nil), d.hover...),
		removed: append([]int(nil), d.removed...),
	}
}

// rollback 撤销试探性追赶：恢复英雄池、选/禁/预选、时钟与备用，并清掉本次占用记录。
func (d *Draft) rollback(cp checkpoint) {
	for _, hero := range d.removed {
		found := false
		for _, old := range cp.removed {
			if old == hero {
				found = true
				break
			}
		}
		if !found {
			d.pool.Restore(hero)
		}
	}
	d.reserve = cp.reserve
	d.step = cp.step
	d.start = cp.start
	d.picks = cp.picks
	d.bans = cp.bans
	d.hover = cp.hover
	d.removed = cp.removed
}
