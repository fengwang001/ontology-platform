package draft

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"testing"

	"ontology/order"
)

type naiveModel struct {
	n       int
	t       int
	steps   []order.Step
	h       int
	reserve [3]int
	step    int
	start   int64
	lastNow int64
	hover   []int
	picked  []Selection
	bans    [3][]int
	used    map[int]bool
}

type naiveOp struct {
	kind   string
	now    int64
	player int
	hero   int
}

func newNaive(t *testing.T, n, b1, c, b2, stepTime int, bk [2]int, h int, now0 int64) *naiveModel {
	t.Helper()
	steps, err := order.New(n, b1, c, b2)
	if err != nil {
		t.Fatal(err)
	}
	return &naiveModel{
		n:       n,
		t:       stepTime,
		steps:   steps,
		h:       h,
		reserve: [3]int{0, bk[0], bk[1]},
		start:   now0,
		lastNow: now0,
		hover:   make([]int, 2*n),
		picked:  make([]Selection, 2*n),
		used:    make(map[int]bool),
	}
}

func (m *naiveModel) advance(now int64) {
	for m.step < len(m.steps) {
		current := m.steps[m.step]
		timeout := m.start + int64(m.t+m.reserve[current.Team])
		if timeout > now {
			return
		}
		for tick := m.start + 1; tick <= timeout; tick++ {
			if tick > now {
				return
			}
			if tick == timeout {
				m.applyAt(tick)
			}
		}
	}
}

func (m *naiveModel) applyAt(timeout int64) {
	current := m.steps[m.step]
	m.reserve[current.Team] = 0
	if current.Kind == order.Pick {
		player := -1
		begin := (current.Team - 1) * m.n
		for candidate := begin; candidate < begin+m.n; candidate++ {
			if m.picked[candidate].Hero == 0 {
				player = candidate
				break
			}
		}
		hero := 1
		if m.hover[player] >= 1 && m.hover[player] <= m.h && !m.used[m.hover[player]] {
			hero = m.hover[player]
		} else {
			for candidate := 1; candidate <= m.h; candidate++ {
				if !m.used[candidate] {
					hero = candidate
					break
				}
			}
		}
		m.used[hero] = true
		m.picked[player] = Selection{Player: player, Hero: hero}
	}
	m.step++
	m.start = timeout
}

func (m *naiveModel) playerTeam(player int) int {
	if player < m.n {
		return 1
	}
	return 2
}

func (m *naiveModel) validPlayer(player int) bool { return player >= 0 && player < 2*m.n }
func (m *naiveModel) validHero(hero int) bool     { return hero >= 1 && hero <= m.h }

func (m *naiveModel) apply(op naiveOp) error {
	if op.now < 0 || op.now > 1_000_000_000_000 {
		return ErrInvalidArgument
	}
	switch op.kind {
	case "advance":
	case "hover":
		if !m.validPlayer(op.player) || !m.validHero(op.hero) {
			return ErrInvalidArgument
		}
	default:
		if !m.validPlayer(op.player) || !m.validHero(op.hero) {
			return ErrInvalidArgument
		}
	}
	if op.now < m.lastNow {
		return ErrClockRewound
	}

	if op.kind == "advance" {
		m.advance(op.now)
		m.lastNow = op.now
		return nil
	}

	if op.kind == "hover" {
		m.advance(op.now)
		m.lastNow = op.now
		if m.step == len(m.steps) {
			return ErrFinished
		}
		if m.picked[op.player].Hero != 0 {
			return ErrPlayerPicked
		}
		m.hover[op.player] = op.hero
		return nil
	}

	saved := m.snapshot()
	m.advance(op.now)
	if m.step == len(m.steps) {
		m.restore(saved)
		return ErrFinished
	}
	current := m.steps[m.step]
	wantedKind := order.Pick
	if op.kind == "ban" {
		wantedKind = order.Ban
	}
	if current.Kind != wantedKind {
		m.restore(saved)
		return ErrWrongStepKind
	}
	if m.playerTeam(op.player) != current.Team {
		m.restore(saved)
		return ErrWrongTeam
	}
	if op.kind == "pick" && m.picked[op.player].Hero != 0 {
		m.restore(saved)
		return ErrPlayerPicked
	}
	if m.used[op.hero] {
		m.restore(saved)
		return ErrHeroUnavailable
	}

	m.used[op.hero] = true
	if op.kind == "ban" {
		m.bans[current.Team] = append(m.bans[current.Team], op.hero)
	} else {
		m.picked[op.player] = Selection{Player: op.player, Hero: op.hero}
	}
	used := int(op.now-m.start) - m.t
	if used > 0 {
		m.reserve[current.Team] -= used
	}
	m.step++
	m.lastNow = op.now
	if m.step < len(m.steps) {
		m.start = op.now
	}
	return nil
}

type naiveSnapshot struct {
	reserve [3]int
	step    int
	start   int64
	hover   []int
	picked  []Selection
	bans    [3][]int
	used    map[int]bool
}

func (m *naiveModel) snapshot() naiveSnapshot {
	var bans [3][]int
	for team := 1; team <= 2; team++ {
		bans[team] = append([]int(nil), m.bans[team]...)
	}
	used := make(map[int]bool, len(m.used))
	for hero, value := range m.used {
		used[hero] = value
	}
	return naiveSnapshot{m.reserve, m.step, m.start, append([]int(nil), m.hover...), append([]Selection(nil), m.picked...), bans, used}
}

func (m *naiveModel) restore(s naiveSnapshot) {
	m.reserve = s.reserve
	m.step = s.step
	m.start = s.start
	m.hover = append([]int(nil), s.hover...)
	m.picked = append([]Selection(nil), s.picked...)
	m.bans = s.bans
	m.used = s.used
}

func (m *naiveModel) state(now int64) State {
	if now >= m.lastNow {
		m.advance(now)
		m.lastNow = now
	}
	team := 0
	if m.step < len(m.steps) {
		team = m.steps[m.step].Team
	}
	picked := make([]Selection, 0, 2*m.n)
	for _, selection := range m.picked {
		if selection.Hero != 0 {
			picked = append(picked, selection)
		}
	}
	var bans [2][]int
	for teamIndex := 1; teamIndex <= 2; teamIndex++ {
		bans[teamIndex-1] = append([]int(nil), m.bans[teamIndex]...)
	}
	return State{Step: m.step, Team: team, Reserve: []int{m.reserve[1], m.reserve[2]}, Picked: picked, Bans: bans}
}

func callController(c *Controller, op naiveOp) error {
	switch op.kind {
	case "ban":
		return c.Ban(op.now, op.player, op.hero)
	case "pick":
		return c.Pick(op.now, op.player, op.hero)
	case "hover":
		return c.Hover(op.now, op.player, op.hero)
	default:
		return c.Advance(op.now)
	}
}

func TestRandomAgainstNaive(t *testing.T) {
	for seed := int64(1); seed <= 1500; seed++ {
		rng := rand.New(rand.NewPCG(uint64(seed), uint64(seed)*7919+1))
		n := 1 + rng.IntN(5)
		b1 := rng.IntN(6)
		b2 := 0
		c := rng.IntN(2*n + 1)
		if c < 2*n {
			b2 = rng.IntN(6)
		}
		heroes := 2*n + 2*(b1+b2) + rng.IntN(12)
		stepTime := 1 + rng.IntN(40)
		bk := [2]int{rng.IntN(50), rng.IntN(50)}
		now0 := rng.Int64N(5)

		controller, err := New(n, b1, c, b2, stepTime, bk, heroes, now0)
		if err != nil {
			t.Fatalf("seed=%d input n=%d b1=%d c=%d b2=%d output new error=%v", seed, n, b1, c, b2, err)
		}
		model := newNaive(t, n, b1, c, b2, stepTime, bk, heroes, now0)
		now := now0

		for action := 0; action < 45; action++ {
			now += int64(rng.IntN(45))
			op := naiveOp{now: now, player: rng.IntN(2 * n), hero: 1 + rng.IntN(heroes+2)}
			switch rng.IntN(4) {
			case 0:
				op.kind = "ban"
			case 1:
				op.kind = "pick"
			case 2:
				op.kind = "hover"
			default:
				op.kind = "advance"
			}
			got := callController(controller, op)
			want := model.apply(op)
			gotState, stateErr := controller.State(now)
			wantState := model.state(now)
			t.Logf("seed=%d input op=%s now=%d player=%d hero=%d output error=%v expected=%v state=%+v expectedState=%+v; reason random replay versus millisecond and hero scanner", seed, op.kind, op.now, op.player, op.hero, got, want, gotState, wantState)
			if !errorsEqual(got, want) || stateErr != nil || !reflect.DeepEqual(gotState, wantState) {
				t.Fatalf("seed=%d mismatch op=%+v got=%v want=%v state=%+v wantState=%+v", seed, op, got, want, gotState, wantState)
			}
		}

		finalTime := now + int64(2*(b1+b2)+2*n)*int64(stepTime+50)
		finalState, _ := controller.State(finalTime)
		modelState := model.state(finalTime)
		if !reflect.DeepEqual(finalState, modelState) || len(finalState.Picked) != 2*n {
			t.Fatalf("seed=%d final got=%+v want=%+v; reason every player gets one hero", seed, finalState, modelState)
		}
	}
}

func errorsEqual(got, want error) bool {
	return fmt.Sprint(got) == fmt.Sprint(want)
}
