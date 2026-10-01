package tournament

import (
	"errors"
	"sync"
)

var (
	ErrInvalidConfig       = errors.New("invalid tournament configuration")
	ErrFixtureNotFound     = errors.New("fixture not found for round and direction")
	ErrInvalidScore        = errors.New("score must be between 0 and 100")
	ErrAlreadyTechnical    = errors.New("fixture already has a technical result")
	ErrAlreadyRecorded     = errors.New("fixture already recorded")
	ErrInvalidTeam         = errors.New("team number is outside the tournament")
	ErrTeamAlreadyWithdrew = errors.New("team already withdrew")
)

type Fixture struct {
	Round int
	Home  int
	Away  int
	Bye   bool
}

type matchKey struct {
	round int
	home  int
	away  int
}

type matchResult struct {
	status    uint8
	homeGoals int
	awayGoals int
}

type teamStats struct {
	points       int
	goalsFor     int
	goalsAgainst int
	played       int
}

type Tournament struct {
	mu        sync.RWMutex
	teams     int
	laps      int
	fixtures  map[int][]Fixture
	matches   map[matchKey]*matchResult
	stats     map[int]*teamStats
	withdrawn map[int]bool
}

func New(teams int, laps int) (*Tournament, error) {
	if teams < 2 || teams > 64 || (laps != 1 && laps != 2) {
		return nil, ErrInvalidConfig
	}

	size := teams
	if size%2 == 1 {
		size++
	}

	tournament := &Tournament{
		teams:     teams,
		laps:      laps,
		fixtures:  make(map[int][]Fixture),
		matches:   make(map[matchKey]*matchResult),
		stats:     make(map[int]*teamStats, teams),
		withdrawn: make(map[int]bool, teams),
	}
	for team := 1; team <= teams; team++ {
		tournament.stats[team] = &teamStats{}
	}

	positions := make([]int, size)
	for index := range positions {
		positions[index] = index + 1
	}

	firstLapRounds := size - 1
	totalRounds := firstLapRounds
	if laps == 2 {
		totalRounds *= 2
	}

	for round := 1; round <= totalRounds; round++ {
		sourceRound := round
		reversed := false
		if round > firstLapRounds {
			sourceRound = round - firstLapRounds
			reversed = true
		}

		currentPositions := append([]int(nil), positions...)
		for rotation := 1; rotation < sourceRound; rotation++ {
			nextPositions := make([]int, size)
			nextPositions[0] = currentPositions[0]
			nextPositions[1] = currentPositions[size-1]
			copy(nextPositions[2:], currentPositions[1:size-1])
			currentPositions = nextPositions
		}

		roundFixtures := make([]Fixture, 0, size/2)
		for index := 0; index < size/2; index++ {
			a := currentPositions[index]
			b := currentPositions[size-1-index]
			home := a
			away := b

			aIsHome := sourceRound%2 == 1
			if index >= 1 {
				aIsHome = (sourceRound+index)%2 == 0
			}
			if !aIsHome {
				home = b
				away = a
			}
			if reversed {
				home, away = away, home
			}

			fixture := Fixture{Round: round, Home: home, Away: away, Bye: teams%2 == 1 && (a == size || b == size)}
			roundFixtures = append(roundFixtures, fixture)
			if !fixture.Bye {
				tournament.matches[matchKey{round: round, home: home, away: away}] = &matchResult{}
			}
		}
		tournament.fixtures[round] = roundFixtures
	}

	return tournament, nil
}

func (t *Tournament) Fixtures(round int) []Fixture {
	t.mu.RLock()
	defer t.mu.RUnlock()

	roundFixtures := t.fixtures[round]
	result := make([]Fixture, len(roundFixtures))
	copy(result, roundFixtures)
	return result
}

func (t *Tournament) Record(round, home, away, homeGoals, awayGoals int) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	match := t.matches[matchKey{round: round, home: home, away: away}]
	if match == nil {
		return ErrFixtureNotFound
	}
	if homeGoals < 0 || homeGoals > 100 || awayGoals < 0 || awayGoals > 100 {
		return ErrInvalidScore
	}
	if match.status == 2 {
		return ErrAlreadyTechnical
	}
	if match.status == 1 {
		return ErrAlreadyRecorded
	}

	match.status = 1
	match.homeGoals = homeGoals
	match.awayGoals = awayGoals
	t.applyResult(home, away, homeGoals, awayGoals)
	return nil
}

func (t *Tournament) Withdraw(team int) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if team < 1 || team > t.teams {
		return ErrInvalidTeam
	}
	if t.withdrawn[team] {
		return ErrTeamAlreadyWithdrew
	}

	for key, match := range t.matches {
		if match.status != 0 {
			continue
		}

		switch team {
		case key.home:
			match.status = 2
			match.homeGoals = 0
			match.awayGoals = 3
			t.applyResult(key.away, key.home, 3, 0)
		case key.away:
			match.status = 2
			match.homeGoals = 3
			match.awayGoals = 0
			t.applyResult(key.home, key.away, 3, 0)
		}
	}

	t.withdrawn[team] = true
	return nil
}

func (t *Tournament) Points(team int) int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if team < 1 || team > t.teams {
		return 0
	}
	return t.stats[team].points
}

func (t *Tournament) GoalDiff(team int) int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if team < 1 || team > t.teams {
		return 0
	}
	return t.stats[team].goalsFor - t.stats[team].goalsAgainst
}

func (t *Tournament) Played(team int) int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if team < 1 || team > t.teams {
		return 0
	}
	return t.stats[team].played
}

func (t *Tournament) Pending() int {
	t.mu.RLock()
	defer t.mu.RUnlock()

	pending := 0
	for _, match := range t.matches {
		if match.status == 0 {
			pending++
		}
	}
	return pending
}

func (t *Tournament) applyResult(home, away, homeGoals, awayGoals int) {
	homeStats := t.stats[home]
	awayStats := t.stats[away]
	homeStats.played++
	awayStats.played++
	homeStats.goalsFor += homeGoals
	homeStats.goalsAgainst += awayGoals
	awayStats.goalsFor += awayGoals
	awayStats.goalsAgainst += homeGoals

	switch {
	case homeGoals > awayGoals:
		homeStats.points += 3
	case homeGoals < awayGoals:
		awayStats.points += 3
	default:
		homeStats.points++
		awayStats.points++
	}
}
