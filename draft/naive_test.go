package draft_test

import (
	"errors"

	"ontology/draft"
	"ontology/order"
)

// naivePool 是朴素英雄池：布尔数组，最小可用英雄从 1 起逐个扫描。
type naivePool struct {
	h    int
	busy []bool // true = 已被禁或已选
}

func newNaivePool(h int) *naivePool {
	return &naivePool{h: h, busy: make([]bool, h+1)}
}

func (p *naivePool) available(hero int) bool {
	return hero >= 1 && hero <= p.h && !p.busy[hero]
}

func (p *naivePool) remove(hero int) bool {
	if !p.available(hero) {
		return false
	}
	p.busy[hero] = true
	return true
}

func (p *naivePool) firstAvailable() (int, bool) {
	for hero := 1; hero <= p.h; hero++ { // 刻意线性扫描，作为“被放弃方案”的对照
		if !p.busy[hero] {
			return hero, true
		}
	}
	return 0, false
}

// naiveDraft 逐毫秒推进：每一步在其逻辑超时时刻立即自动处理。
// 被拒操作同样让逻辑时钟走到 now（入口处理只由 now 与已接受操作决定）。
type naiveDraft struct {
	n, b1, c, b2 int
	t, bk        int64
	h            int
	now0         int64
	steps        []order.Step

	pool    *naivePool
	reserve [3]int64
	step    int
	start   int64
	picks   []int
	bans    [2][]int
	hover   []int
	lastNow int64
}

func newNaive(n, b1, c, b2 int, t, bk int64, h int, now0 int64) *naiveDraft {
	d := &naiveDraft{
		n: n, b1: b1, c: c, b2: b2, t: t, bk: bk, h: h, now0: now0,
		steps:   order.Build(order.Config{N: n, B1: b1, C: c, B2: b2}),
		pool:    newNaivePool(h),
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
	d.start = now0
	return d
}

func (d *naiveDraft) deadline() int64 {
	return d.start + d.t + d.reserve[d.steps[d.step].Team]
}

func (d *naiveDraft) autoPick(team int) {
	lo := (team - 1) * d.n
	player := lo
	for player < lo+d.n && d.picks[player] != -1 {
		player++
	}
	hero := d.hover[player]
	if hero == -1 || !d.pool.available(hero) {
		h, ok := d.pool.firstAvailable()
		if !ok {
			return
		}
		hero = h
	}
	d.pool.remove(hero)
	d.picks[player] = hero
}

// advanceTo 推进逻辑时钟到 now，依次自动处理所有超时时刻不大于 now 的步。
func (d *naiveDraft) advanceTo(now int64) {
	for d.step < len(d.steps) && d.deadline() <= now {
		x := d.deadline()
		team := d.steps[d.step].Team
		d.reserve[team] = 0
		if d.steps[d.step].Kind == order.KindPick {
			d.autoPick(team)
		} // 禁用步超时 = 空禁
		d.step++
		d.start = x
	}
}

func naiveValidNow(now int64) bool { return now >= 0 && now <= 1_000_000_000_000 }

func (d *naiveDraft) onActingTeam(player int) bool {
	team := d.steps[d.step].Team
	lo := (team - 1) * d.n
	return player >= lo && player < lo+d.n
}

func (d *naiveDraft) rewind(now int64) bool {
	return now < d.now0 || now < d.lastNow
}

func (d *naiveDraft) manual(now int64, player, hero int, pick bool) error {
	if !naiveValidNow(now) || player < 0 || player >= 2*d.n || hero < 1 || hero > d.h {
		return draft.ErrInvalid
	}
	if d.rewind(now) {
		return draft.ErrClockRewind
	}
	d.advanceTo(now)
	if d.step >= len(d.steps) {
		return draft.ErrFinished
	}
	isBan := d.steps[d.step].Kind == order.KindBan
	if pick == isBan {
		return draft.ErrWrongKind
	}
	if !d.onActingTeam(player) {
		return draft.ErrWrongTeam
	}
	if pick && d.picks[player] != -1 {
		return draft.ErrAlreadyPick
	}
	if !d.pool.available(hero) {
		return draft.ErrHeroBusy
	}
	team := d.steps[d.step].Team
	if elapsed := now - d.start - d.t; elapsed > 0 {
		d.reserve[team] -= elapsed
	}
	d.pool.remove(hero)
	if isBan {
		d.bans[team-1] = append(d.bans[team-1], hero)
	} else {
		d.picks[player] = hero
	}
	d.step++
	d.start = now
	return nil
}

func (d *naiveDraft) hoverOp(now int64, player, hero int) error {
	if !naiveValidNow(now) || player < 0 || player >= 2*d.n || hero < 1 || hero > d.h {
		return draft.ErrInvalid
	}
	if d.rewind(now) {
		return draft.ErrClockRewind
	}
	d.advanceTo(now)
	if d.step >= len(d.steps) {
		return draft.ErrFinished
	}
	if d.picks[player] != -1 {
		return draft.ErrAlreadyPick
	}
	d.hover[player] = hero
	return nil
}

func (d *naiveDraft) advance(now int64) error {
	if !naiveValidNow(now) {
		return draft.ErrInvalid
	}
	if d.rewind(now) {
		return draft.ErrClockRewind
	}
	d.advanceTo(now)
	d.lastNow = now
	return nil
}

func eqErr(a, b error) bool {
	if a == nil || b == nil {
		return a == b
	}
	return errors.Is(a, b) || errors.Is(b, a)
}

func errName(err error) string {
	switch {
	case errors.Is(err, draft.ErrInvalid):
		return "invalid"
	case errors.Is(err, draft.ErrClockRewind):
		return "rewind"
	case errors.Is(err, draft.ErrFinished):
		return "finished"
	case errors.Is(err, draft.ErrWrongKind):
		return "wrong-kind"
	case errors.Is(err, draft.ErrWrongTeam):
		return "wrong-team"
	case errors.Is(err, draft.ErrAlreadyPick):
		return "already-picked"
	case errors.Is(err, draft.ErrHeroBusy):
		return "hero-busy"
	default:
		return err.Error()
	}
}

// peek 镜像生产 State：时钟回退检查、入口追赶并提交 lastNow，返回同构快照。
func (d *naiveDraft) peek(now int64) (*draft.State, error) {
	if !naiveValidNow(now) {
		return nil, draft.ErrInvalid
	}
	if now < d.now0 || now < d.lastNow {
		return nil, draft.ErrClockRewind
	}
	step := d.step
	start := d.start
	reserve := d.reserve
	picks := append([]int(nil), d.picks...)
	bans0 := append([]int(nil), d.bans[0]...)
	bans1 := append([]int(nil), d.bans[1]...)
	busy := append([]bool(nil), d.pool.busy...)

	local := &naiveDraft{
		n: d.n, h: d.h, steps: d.steps, t: d.t,
		pool: &naivePool{h: d.h, busy: busy},
	}
	local.b1, local.b2, local.c = d.b1, d.b2, d.c
	local.step, local.start, local.reserve, local.picks = step, start, reserve, picks
	local.bans[0], local.bans[1] = bans0, bans1
	local.hover = append([]int(nil), d.hover...)
	local.advanceTo(now)

	d.step, d.start, d.reserve, d.picks = local.step, local.start, local.reserve, local.picks
	d.bans[0], d.bans[1] = local.bans[0], local.bans[1]
	d.pool.busy = busy
	d.lastNow = now

	st := &draft.State{
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
