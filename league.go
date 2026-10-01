package ontology

import (
	"sort"
	"sync"
)

var (
	ErrInvalidTeam       = invalidTeamError{}
	ErrSameTeam          = sameTeamError{}
	ErrInvalidScore      = invalidScoreError{}
	ErrMatchAlreadyAdded = matchAlreadyAddedError{}
	ErrMatchNotAdded     = matchNotAddedError{}
)

type invalidTeamError struct{}

func (invalidTeamError) Error() string { return "team number is out of range" }

type sameTeamError struct{}

func (sameTeamError) Error() string { return "home and away team must differ" }

type invalidScoreError struct{}

func (invalidScoreError) Error() string { return "score must be between 0 and 100" }

type matchAlreadyAddedError struct{}

func (matchAlreadyAddedError) Error() string { return "match has already been added" }

type matchNotAddedError struct{}

func (matchNotAddedError) Error() string { return "match has not been added" }

type match struct {
	homeGoals int
	awayGoals int
}

type Standing struct {
	Team     int
	Rank     int
	Points   int
	GoalDiff int
	GoalsFor int
}

type totals struct {
	points   int
	goalDiff int
	goalsFor int
}

type League struct {
	mu    sync.RWMutex
	teams int
	games map[[2]int]match
}

func (l *League) validTeam(team int) bool {
	return team >= 1 && team <= l.teams
}

func NewLeague(n int) (*League, error) {
	if n < 2 || n > 32 {
		return nil, invalidTeamError{}
	}
	return &League{teams: n, games: make(map[[2]int]match)}, nil
}

func (l *League) Add(home, away, homeGoals, awayGoals int) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.validTeam(home) || !l.validTeam(away) {
		return ErrInvalidTeam
	}
	if home == away {
		return ErrSameTeam
	}
	if homeGoals < 0 || homeGoals > 100 || awayGoals < 0 || awayGoals > 100 {
		return ErrInvalidScore
	}
	key := [2]int{home, away}
	if _, exists := l.games[key]; exists {
		return ErrMatchAlreadyAdded
	}
	l.games[key] = match{homeGoals: homeGoals, awayGoals: awayGoals}
	return nil
}

func (l *League) Remove(home, away int) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.validTeam(home) || !l.validTeam(away) {
		return ErrInvalidTeam
	}
	key := [2]int{home, away}
	if _, exists := l.games[key]; !exists {
		return ErrMatchNotAdded
	}
	delete(l.games, key)
	return nil
}

func (l *League) Table() []Standing {
	l.mu.RLock()
	defer l.mu.RUnlock()

	teamTotals := l.totals()
	blocks := l.rankBlocks(teamTotals)
	ranks := ranksFromBlocks(blocks)

	table := make([]Standing, 0, l.teams)
	for _, block := range blocks {
		teams := append([]int(nil), block...)
		sort.Ints(teams)
		for _, team := range teams {
			teamTotal := teamTotals[team]
			table = append(table, Standing{
				Team:     team,
				Rank:     ranks[team],
				Points:   teamTotal.points,
				GoalDiff: teamTotal.goalDiff,
				GoalsFor: teamTotal.goalsFor,
			})
		}
	}
	return table
}

func ranksFromBlocks(blocks [][]int) []int {
	ranks := make([]int, 33)
	placed := 0
	for _, block := range blocks {
		rank := placed + 1
		for _, team := range block {
			ranks[team] = rank
		}
		placed += len(block)
	}
	return ranks
}

func (l *League) rankBlocks(teamTotals []totals) [][]int {
	groups := partitionBy(teamIDs(l.teams), func(team int) [3]int {
		return [3]int{teamTotals[team].points, 0, 0}
	})
	blocks := make([][]int, 0, l.teams)
	for _, group := range groups {
		blocks = append(blocks, l.rankGroup(group, teamTotals)...)
	}
	return blocks
}

func teamIDs(n int) []int {
	teams := make([]int, n)
	for team := range teams {
		teams[team] = team + 1
	}
	return teams
}

func (l *League) rankGroup(group []int, teamTotals []totals) [][]int {
	if len(group) < 2 {
		return [][]int{group}
	}

	mutualTotals := l.internalTotals(group)
	classes := partitionBy(group, func(team int) [3]int {
		return [3]int{
			mutualTotals[team].points,
			mutualTotals[team].goalDiff,
			mutualTotals[team].goalsFor,
		}
	})
	if len(classes) >= 2 {
		return l.rankClasses(classes, teamTotals)
	}

	classes = partitionBy(group, func(team int) [3]int {
		return [3]int{
			teamTotals[team].goalDiff,
			teamTotals[team].goalsFor,
			0,
		}
	})
	if len(classes) >= 2 {
		return l.rankClasses(classes, teamTotals)
	}

	return [][]int{group}
}

func (l *League) rankClasses(classes [][]int, teamTotals []totals) [][]int {
	blocks := make([][]int, 0)
	for _, class := range classes {
		if len(class) < 2 {
			tableClass := append([]int(nil), class...)
			blocks = append(blocks, tableClass)
			continue
		}
		blocks = append(blocks, l.rankGroup(class, teamTotals)...)
	}
	return blocks
}

func partitionBy(teams []int, key func(int) [3]int) [][]int {
	ordered := append([]int(nil), teams...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left := key(ordered[i])
		right := key(ordered[j])
		for index := range left {
			if left[index] != right[index] {
				return left[index] > right[index]
			}
		}
		return false
	})

	classes := make([][]int, 0)
	for _, team := range ordered {
		teamKey := key(team)
		if len(classes) == 0 {
			classes = append(classes, []int{team})
			continue
		}
		last := &classes[len(classes)-1]
		if key((*last)[0]) != teamKey {
			classes = append(classes, []int{team})
			continue
		}
		*last = append(*last, team)
	}
	return classes
}

func (l *League) internalTotals(teams []int) []totals {
	inGroup := make([]bool, l.teams+1)
	for _, team := range teams {
		inGroup[team] = true
	}

	teamTotals := make([]totals, l.teams+1)
	for key, game := range l.games {
		home, away := key[0], key[1]
		if !inGroup[home] || !inGroup[away] {
			continue
		}
		homeTotals := &teamTotals[home]
		awayTotals := &teamTotals[away]
		homeTotals.goalsFor += game.homeGoals
		awayTotals.goalsFor += game.awayGoals
		homeTotals.goalDiff += game.homeGoals - game.awayGoals
		awayTotals.goalDiff += game.awayGoals - game.homeGoals
		switch {
		case game.homeGoals > game.awayGoals:
			homeTotals.points += 3
		case game.homeGoals < game.awayGoals:
			awayTotals.points += 3
		default:
			homeTotals.points++
			awayTotals.points++
		}
	}
	return teamTotals
}

func (l *League) RankOf(team int) (int, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if !l.validTeam(team) {
		return 0, ErrInvalidTeam
	}
	return ranksFromBlocks(l.rankBlocks(l.totals()))[team], nil
}

func (l *League) totals() []totals {
	teamTotals := make([]totals, l.teams+1)
	for key, game := range l.games {
		home, away := key[0], key[1]
		homeTotals := &teamTotals[home]
		awayTotals := &teamTotals[away]
		homeTotals.goalsFor += game.homeGoals
		awayTotals.goalsFor += game.awayGoals
		homeTotals.goalDiff += game.homeGoals - game.awayGoals
		awayTotals.goalDiff += game.awayGoals - game.homeGoals
		switch {
		case game.homeGoals > game.awayGoals:
			homeTotals.points += 3
		case game.homeGoals < game.awayGoals:
			awayTotals.points += 3
		default:
			homeTotals.points++
			awayTotals.points++
		}
	}
	return teamTotals
}
