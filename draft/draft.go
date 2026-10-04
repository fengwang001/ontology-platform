package draft

import (
	"errors"
	"sync"

	"ontology/order"
	"ontology/pool"
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrClockRewound    = errors.New("clock rewound")
	ErrFinished        = errors.New("draft finished")
	ErrWrongStepKind   = errors.New("wrong step kind")
	ErrWrongTeam       = errors.New("player is not on acting team")
	ErrPlayerPicked    = errors.New("player already picked")
	ErrHeroUnavailable = errors.New("hero unavailable")
)

type Selection struct {
	Player int
	Hero   int
}

type State struct {
	Step    int
	Team    int
	Reserve []int
	Picked  []Selection
	Bans    [2][]int
}

type Controller struct {
	mu      sync.Mutex
	n       int
	t       int
	h       int
	steps   []order.Step
	heroes  *pool.Pool
	reserve [3]int
	step    int
	start   int64
	lastNow int64
	hover   []int
	picked  []Selection
	bans    [3][]int
}

type snapshot struct {
	heroes  *pool.Pool
	reserve [3]int
	step    int
	start   int64
	hover   []int
	picked  []Selection
	bans    [3][]int
}

func (c *Controller) snapshot() snapshot {
	var copiedBans [3][]int
	for team := 1; team <= 2; team++ {
		copiedBans[team] = append([]int(nil), c.bans[team]...)
	}
	return snapshot{
		heroes:  c.heroes.Clone(),
		reserve: c.reserve,
		step:    c.step,
		start:   c.start,
		hover:   append([]int(nil), c.hover...),
		picked:  append([]Selection(nil), c.picked...),
		bans:    copiedBans,
	}
}

func (c *Controller) restore(s snapshot) {
	c.heroes.Restore(s.heroes)
	c.reserve = s.reserve
	c.step = s.step
	c.start = s.start
	c.hover = append([]int(nil), s.hover...)
	c.picked = append([]Selection(nil), s.picked...)
	for team := 1; team <= 2; team++ {
		c.bans[team] = append([]int(nil), s.bans[team]...)
	}
}

func New(n, b1, c, b2, stepTime int, bk [2]int, heroCount int, now0 int64) (*Controller, error) {
	if stepTime < 1 || stepTime > 1_000_000 || now0 < 0 || now0 > 1_000_000_000_000 {
		return nil, ErrInvalidArgument
	}
	if bk[0] < 0 || bk[0] > 1_000_000 || bk[1] < 0 || bk[1] > 1_000_000 {
		return nil, ErrInvalidArgument
	}
	minHeroes := 2*n + 2*(b1+b2)
	if heroCount < minHeroes || heroCount > 100_000 {
		return nil, ErrInvalidArgument
	}
	steps, err := order.New(n, b1, c, b2)
	if err != nil {
		return nil, err
	}
	return &Controller{
		n:       n,
		t:       stepTime,
		h:       heroCount,
		steps:   steps,
		heroes:  pool.New(heroCount),
		reserve: [3]int{0, bk[0], bk[1]},
		start:   now0,
		lastNow: now0,
		hover:   make([]int, 2*n),
		picked:  make([]Selection, 2*n),
	}, nil
}

func (c *Controller) Ban(now int64, player, hero int) error {
	if !c.validPlayer(player) || !c.validHero(hero) || !c.validNow(now) {
		return ErrInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if now < c.lastNow {
		return ErrClockRewound
	}
	restore := c.snapshot()
	c.advanceTo(now)
	if c.finished() {
		c.restore(restore)
		return ErrFinished
	}
	current := c.steps[c.step]
	if current.Kind != order.Ban {
		c.restore(restore)
		return ErrWrongStepKind
	}
	if c.playerTeam(player) != current.Team {
		c.restore(restore)
		return ErrWrongTeam
	}
	if err := c.heroes.Ban(hero, current.Team); err != nil {
		c.restore(restore)
		return mapPoolError(err)
	}
	c.bans[current.Team] = append(c.bans[current.Team], hero)
	c.acceptManual(now)
	c.lastNow = now
	return nil
}

func (c *Controller) Pick(now int64, player, hero int) error {
	if !c.validPlayer(player) || !c.validHero(hero) || !c.validNow(now) {
		return ErrInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if now < c.lastNow {
		return ErrClockRewound
	}
	restore := c.snapshot()
	c.advanceTo(now)
	if c.finished() {
		c.restore(restore)
		return ErrFinished
	}
	current := c.steps[c.step]
	if current.Kind != order.Pick {
		c.restore(restore)
		return ErrWrongStepKind
	}
	if c.playerTeam(player) != current.Team {
		c.restore(restore)
		return ErrWrongTeam
	}
	if c.picked[player].Hero != 0 {
		c.restore(restore)
		return ErrPlayerPicked
	}
	if err := c.heroes.Pick(hero, current.Team, player); err != nil {
		c.restore(restore)
		return mapPoolError(err)
	}
	c.picked[player] = Selection{Player: player, Hero: hero}
	c.acceptManual(now)
	c.lastNow = now
	return nil
}

func (c *Controller) Hover(now int64, player, hero int) error {
	if !c.validPlayer(player) || !c.validHero(hero) || !c.validNow(now) {
		return ErrInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if now < c.lastNow {
		return ErrClockRewound
	}
	c.advanceTo(now)
	c.lastNow = now
	if c.finished() {
		return ErrFinished
	}
	if c.picked[player].Hero != 0 {
		return ErrPlayerPicked
	}
	c.hover[player] = hero
	return nil
}

func (c *Controller) Advance(now int64) error {
	if !c.validNow(now) {
		return ErrInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if now < c.lastNow {
		return ErrClockRewound
	}
	c.advanceTo(now)
	c.lastNow = now
	return nil
}

func (c *Controller) State(now int64) (State, error) {
	if !c.validNow(now) {
		return State{}, ErrInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if now < c.lastNow {
		return State{}, ErrClockRewound
	}
	c.advanceTo(now)
	c.lastNow = now

	team := 0
	if c.step < len(c.steps) {
		team = c.steps[c.step].Team
	}
	picked := make([]Selection, 0, 2*c.n)
	for _, selection := range c.picked {
		if selection.Hero != 0 {
			picked = append(picked, selection)
		}
	}
	var bans [2][]int
	for teamIndex := 1; teamIndex <= 2; teamIndex++ {
		bans[teamIndex-1] = append([]int(nil), c.bans[teamIndex]...)
	}
	return State{
		Step:    c.step,
		Team:    team,
		Reserve: append([]int(nil), c.reserve[1], c.reserve[2]),
		Picked:  picked,
		Bans:    bans,
	}, nil
}

func (c *Controller) validPlayer(player int) bool {
	return player >= 0 && player < 2*c.n
}

func (c *Controller) validHero(hero int) bool {
	return hero >= 1 && hero <= c.h
}

func (c *Controller) validNow(now int64) bool {
	return now >= 0 && now <= 1_000_000_000_000
}

func (c *Controller) playerTeam(player int) int {
	if player < c.n {
		return 1
	}
	return 2
}

func (c *Controller) finished() bool {
	return c.step >= len(c.steps)
}

func (c *Controller) acceptManual(now int64) {
	team := c.steps[c.step].Team
	used := int(now-c.start) - c.t
	if used > 0 {
		c.reserve[team] -= used
	}
	c.step++
	if !c.finished() {
		c.start = now
	}
}

func (c *Controller) advanceTo(now int64) {
	for !c.finished() {
		team := c.steps[c.step].Team
		timeout := c.start + int64(c.t+c.reserve[team])
		if timeout > now {
			return
		}
		c.applyTimeout(timeout)
	}
}

func (c *Controller) applyTimeout(timeout int64) {
	current := c.steps[c.step]
	c.reserve[current.Team] = 0
	if current.Kind == order.Pick {
		player := c.freePlayer(current.Team)
		hero, _, _ := c.heroes.Choose(c.hover[player])
		_ = c.heroes.Pick(hero, current.Team, player)
		c.picked[player] = Selection{Player: player, Hero: hero}
	}
	c.step++
	c.start = timeout
}

func (c *Controller) freePlayer(team int) int {
	begin := (team - 1) * c.n
	for player := begin; player < begin+c.n; player++ {
		if c.picked[player].Hero == 0 {
			return player
		}
	}
	return -1
}

func mapPoolError(err error) error {
	if err == pool.ErrUnavailable {
		return ErrHeroUnavailable
	}
	return ErrInvalidArgument
}
