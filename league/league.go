package league

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrInvalidTeam   = errors.New("invalid team")
	ErrSameTeam      = errors.New("home and away must differ")
	ErrInvalidScore  = errors.New("score must be between 0 and 100")
	ErrMatchExists   = errors.New("match already registered")
	ErrMatchNotExist = errors.New("match not registered")
)

type Row struct {
	Team     int
	Rank     int
	Points   int
	GoalDiff int
	GoalsFor int
}

type result struct {
	homeGoals int
	awayGoals int
}

type stats struct {
	points   int
	goalDiff int
	goalsFor int
}

type Table struct {
	mu      sync.RWMutex
	n       int
	matches map[[2]int]result
}

func New(n int) (*Table, error) {
	if n < 2 || n > 32 {
		return nil, ErrInvalidTeam
	}
	return &Table{
		n:       n,
		matches: make(map[[2]int]result),
	}, nil
}

func (t *Table) Add(home, away, homeGoals, awayGoals int) error {
	if !t.validTeam(home) || !t.validTeam(away) {
		return ErrInvalidTeam
	}
	if home == away {
		return ErrSameTeam
	}
	if homeGoals < 0 || homeGoals > 100 || awayGoals < 0 || awayGoals > 100 {
		return ErrInvalidScore
	}

	key := [2]int{home, away}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.matches[key]; ok {
		return ErrMatchExists
	}
	t.matches[key] = result{homeGoals: homeGoals, awayGoals: awayGoals}
	return nil
}

func (t *Table) Remove(home, away int) error {
	if !t.validTeam(home) || !t.validTeam(away) {
		return ErrInvalidTeam
	}

	key := [2]int{home, away}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.matches[key]; !ok {
		return ErrMatchNotExist
	}
	delete(t.matches, key)
	return nil
}

func (t *Table) Table() []Row {
	t.mu.RLock()
	matches := make(map[[2]int]result, len(t.matches))
	for key, value := range t.matches {
		matches[key] = value
	}
	n := t.n
	t.mu.RUnlock()

	full := scopeStats(n, matches, allTeams(n))
	blocks := rankedBlocks(n, matches, full)
	rows := make([]Row, 0, n)
	position := 0
	for _, tiedTeams := range blocks {
		rank := position + 1
		for _, team := range tiedTeams {
			position++
			rows = append(rows, Row{
				Team:     team,
				Rank:     rank,
				Points:   full[team].points,
				GoalDiff: full[team].goalDiff,
				GoalsFor: full[team].goalsFor,
			})
		}
	}
	return rows
}

func (t *Table) RankOf(team int) (int, error) {
	if !t.validTeam(team) {
		return 0, ErrInvalidTeam
	}
	for _, row := range t.Table() {
		if row.Team == team {
			return row.Rank, nil
		}
	}
	panic("league: ranking omitted a valid team")
}

func (t *Table) validTeam(team int) bool {
	return team >= 1 && team <= t.n
}

func allTeams(n int) []int {
	teams := make([]int, n)
	for team := range teams {
		teams[team] = team + 1
	}
	return teams
}

func scopeStats(n int, matches map[[2]int]result, teams []int) map[int]stats {
	values := make(map[int]stats, n)
	inScope := make(map[int]bool, len(teams))
	for _, team := range teams {
		inScope[team] = true
	}

	for key, match := range matches {
		home := key[0]
		away := key[1]
		if !inScope[home] || !inScope[away] {
			continue
		}

		homeStats := values[home]
		awayStats := values[away]
		homeStats.goalDiff += match.homeGoals - match.awayGoals
		awayStats.goalDiff += match.awayGoals - match.homeGoals
		homeStats.goalsFor += match.homeGoals
		awayStats.goalsFor += match.awayGoals
		switch {
		case match.homeGoals > match.awayGoals:
			homeStats.points += 3
		case match.homeGoals < match.awayGoals:
			awayStats.points += 3
		default:
			homeStats.points++
			awayStats.points++
		}
		values[home] = homeStats
		values[away] = awayStats
	}
	return values
}

func rankedBlocks(n int, matches map[[2]int]result, full map[int]stats) [][]int {
	groups := partitionByPoints(allTeams(n), full)

	blocks := make([][]int, 0, n)
	for _, group := range groups {
		blocks = append(blocks, rankGroup(group.teams, matches, full)...)
	}
	return blocks
}

func rankGroup(teams []int, matches map[[2]int]result, full map[int]stats) [][]int {
	if len(teams) <= 1 {
		return [][]int{append([]int(nil), teams...)}
	}

	local := scopeStats(len(teams), matches, teams)
	classes := partitionHeadToHead(teams, local)
	if len(classes) == 1 {
		classes = partitionByGoals(teams, full)
	}
	if len(classes) == 1 {
		tied := append([]int(nil), teams...)
		sort.Ints(tied)
		return [][]int{tied}
	}

	blocks := make([][]int, 0, len(teams))
	for _, class := range classes {
		blocks = append(blocks, rankGroup(class.teams, matches, full)...)
	}
	return blocks
}

type statsClass struct {
	key   stats
	teams []int
}

type classKeyMode int

const (
	classByPoints classKeyMode = iota
	classHeadToHead
	classByGoals
)

func partitionByPoints(teams []int, values map[int]stats) []statsClass {
	return splitClasses(teams, values, classByPoints)
}

func partitionHeadToHead(teams []int, values map[int]stats) []statsClass {
	return splitClasses(teams, values, classHeadToHead)
}

func partitionByGoals(teams []int, values map[int]stats) []statsClass {
	return splitClasses(teams, values, classByGoals)
}

func splitClasses(teams []int, values map[int]stats, mode classKeyMode) []statsClass {
	sorted := append([]int(nil), teams...)
	sort.Slice(sorted, func(i, j int) bool {
		left := values[sorted[i]]
		right := values[sorted[j]]
		if mode != classByGoals && left.points != right.points {
			return left.points > right.points
		}
		if left.goalDiff != right.goalDiff {
			if mode == classByPoints {
				return sorted[i] < sorted[j]
			}
			return left.goalDiff > right.goalDiff
		}
		if left.goalsFor != right.goalsFor {
			if mode == classByPoints {
				return sorted[i] < sorted[j]
			}
			return left.goalsFor > right.goalsFor
		}
		return sorted[i] < sorted[j]
	})

	classes := make([]statsClass, 0)
	for _, team := range sorted {
		key := stats{}
		switch mode {
		case classByPoints:
			key.points = values[team].points
		case classHeadToHead:
			key = values[team]
		case classByGoals:
			key.goalDiff = values[team].goalDiff
			key.goalsFor = values[team].goalsFor
		}
		if len(classes) == 0 || classes[len(classes)-1].key != key {
			classes = append(classes, statsClass{key: key, teams: []int{team}})
		} else {
			classes[len(classes)-1].teams = append(classes[len(classes)-1].teams, team)
		}
	}
	return classes
}
