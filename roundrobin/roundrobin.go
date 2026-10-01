// Package roundrobin creates circle-method round-robin schedules and records results.
package roundrobin

import (
	"errors"
	"sync"
)

var (
	// ErrInvalidN means the team count is outside [2, 64].
	ErrInvalidN = errors.New("invalid team count")
	// ErrInvalidL means the lap count is neither 1 nor 2.
	ErrInvalidL = errors.New("invalid lap count")
	// ErrInvalidFixture means the round, oriented fixture, or bye cannot accept the record.
	ErrInvalidFixture = errors.New("invalid fixture")
	// ErrInvalidScore means a goal count is negative or greater than 100.
	ErrInvalidScore = errors.New("invalid score")
	// ErrAlreadyDecided means the fixture already has a withdrawal-based 3:0 result.
	ErrAlreadyDecided = errors.New("fixture already technically decided")
	// ErrAlreadyRecorded means the fixture already has an explicitly recorded score.
	ErrAlreadyRecorded = errors.New("fixture already recorded")
	// ErrInvalidTeam means the number is not one of the real teams.
	ErrInvalidTeam = errors.New("invalid team")
	// ErrAlreadyWithdrawn means the team has already withdrawn.
	ErrAlreadyWithdrawn = errors.New("team already withdrawn")
)

// Fixture describes one scheduled pairing in round order.
type Fixture struct {
	Round int
	Index int
	Home  int
	Away  int
	Bye   bool
}

type fixtureState struct {
	recorded bool
	decided  bool
	homeGoal int
	awayGoal int
}

type teamStats struct {
	points       int
	goalsFor     int
	goalsAgainst int
	played       int
}

type Registrar struct {
	mu        sync.RWMutex
	teamCount int
	size      int
	laps      int
	rounds    int
	fixtures  [][]Fixture
	states    [][]fixtureState
	stats     []teamStats
	withdrawn []bool
}

// NewRegistrar creates a single or double round-robin schedule and result table.
func NewRegistrar(n int, l int) (*Registrar, error) {
	if n < 2 || n > 64 {
		return nil, ErrInvalidN
	}
	if l != 1 && l != 2 {
		return nil, ErrInvalidL
	}

	size := n
	if size%2 == 1 {
		size++
	}

	firstLap := make([][]Fixture, size-1)
	positions := make([]int, size)
	for i := range positions {
		positions[i] = i + 1
	}

	for round := 1; round <= size-1; round++ {
		firstLap[round-1] = buildRound(n, round, positions)
		next := make([]int, size)
		next[0] = positions[0]
		next[1] = positions[size-1]
		copy(next[2:], positions[1:size-1])
		positions = next
	}

	fixtures := firstLap
	if l == 2 {
		fixtures = append(fixtures, make([][]Fixture, size-1)...)
		for round := range firstLap {
			reversed := make([]Fixture, len(firstLap[round]))
			for i, fixture := range firstLap[round] {
				reversed[i] = Fixture{
					Round: size - 1 + round + 1,
					Index: fixture.Index,
					Home:  fixture.Away,
					Away:  fixture.Home,
					Bye:   fixture.Bye,
				}
			}
			fixtures[size-1+round] = reversed
		}
	}

	states := make([][]fixtureState, l*(size-1))
	for round := range states {
		states[round] = make([]fixtureState, size/2)
	}

	return &Registrar{
		teamCount: n,
		size:      size,
		laps:      l,
		rounds:    l * (size - 1),
		fixtures:  fixtures,
		states:    states,
		stats:     make([]teamStats, n+1),
		withdrawn: make([]bool, n+1),
	}, nil
}

func buildRound(teamCount int, round int, positions []int) []Fixture {
	fixtures := make([]Fixture, len(positions)/2)
	for i := 0; i < len(positions)/2; i++ {
		a := positions[i]
		b := positions[len(positions)-1-i]
		home := a
		away := b
		if i == 0 {
			if round%2 == 0 {
				home, away = away, home
			}
		} else if (round+i)%2 != 0 {
			home, away = away, home
		}
		virtualTeam := len(positions)
		isBye := teamCount%2 == 1 && (a == virtualTeam || b == virtualTeam)

		fixtures[i] = Fixture{
			Round: round,
			Index: i,
			Home:  home,
			Away:  away,
			Bye:   isBye,
		}
	}
	return fixtures
}

// Fixtures returns a copy of the ordered fixtures for the requested round.
func (r *Registrar) Fixtures(round int) []Fixture {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if round < 1 || round > r.rounds {
		return nil
	}
	return append([]Fixture(nil), r.fixtures[round-1]...)
}

// Record validates and stores a fixture result with the specified home orientation.
func (r *Registrar) Record(round, home, away, homeGoals, awayGoals int) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	index := r.findFixtureLocked(round, home, away)
	if index < 0 {
		return ErrInvalidFixture
	}
	if homeGoals < 0 || homeGoals > 100 || awayGoals < 0 || awayGoals > 100 {
		return ErrInvalidScore
	}

	state := &r.states[round-1][index]
	if state.decided {
		return ErrAlreadyDecided
	}
	if state.recorded {
		return ErrAlreadyRecorded
	}

	state.recorded = true
	state.homeGoal = homeGoals
	state.awayGoal = awayGoals
	r.applyResult(home, away, homeGoals, awayGoals)
	return nil
}

// Withdraw immediately awards every undecided fixture of a team to its opponent by 3:0.
func (r *Registrar) Withdraw(team int) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.validTeam(team) {
		return ErrInvalidTeam
	}
	if r.withdrawn[team] {
		return ErrAlreadyWithdrawn
	}

	r.withdrawn[team] = true
	for round := range r.fixtures {
		for index, fixture := range r.fixtures[round] {
			if fixture.Bye {
				continue
			}

			state := &r.states[round][index]
			if state.recorded || state.decided {
				continue
			}

			switch team {
			case fixture.Home:
				state.decided = true
				state.homeGoal = 0
				state.awayGoal = 3
				r.applyResult(fixture.Home, fixture.Away, 0, 3)
			case fixture.Away:
				state.decided = true
				state.homeGoal = 3
				state.awayGoal = 0
				r.applyResult(fixture.Home, fixture.Away, 3, 0)
			}
		}
	}
	return nil
}

// Points returns a real team's accumulated points.
func (r *Registrar) Points(team int) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.validTeam(team) {
		return 0
	}
	return r.stats[team].points
}

// GoalDiff returns a real team's goals for minus goals against.
func (r *Registrar) GoalDiff(team int) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.validTeam(team) {
		return 0
	}
	stats := r.stats[team]
	return stats.goalsFor - stats.goalsAgainst
}

// Played returns the number of recorded or technically decided fixtures for a team.
func (r *Registrar) Played(team int) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.validTeam(team) {
		return 0
	}
	return r.stats[team].played
}

// Pending returns the number of real fixtures that are neither recorded nor decided.
func (r *Registrar) Pending() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	pending := 0
	for round := range r.fixtures {
		for i, fixture := range r.fixtures[round] {
			if fixture.Bye {
				continue
			}
			state := r.states[round][i]
			if !state.recorded && !state.decided {
				pending++
			}
		}
	}
	return pending
}

func (r *Registrar) findFixtureLocked(round, home, away int) int {
	if round < 1 || round > r.rounds {
		return -1
	}
	for index, fixture := range r.fixtures[round-1] {
		if !fixture.Bye && fixture.Home == home && fixture.Away == away {
			return index
		}
	}
	return -1
}

func (r *Registrar) applyResult(home, away, homeGoals, awayGoals int) {
	r.stats[home].goalsFor += homeGoals
	r.stats[home].goalsAgainst += awayGoals
	r.stats[home].played++
	r.stats[away].goalsFor += awayGoals
	r.stats[away].goalsAgainst += homeGoals
	r.stats[away].played++

	switch {
	case homeGoals > awayGoals:
		r.stats[home].points += 3
	case homeGoals < awayGoals:
		r.stats[away].points += 3
	default:
		r.stats[home].points++
		r.stats[away].points++
	}
}

func (r *Registrar) validTeam(team int) bool {
	return team >= 1 && team <= r.teamCount
}
