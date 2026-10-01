package league

import (
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

type oracleMatch struct {
	home, away, homeGoals, awayGoals int
}

type oracleStats struct {
	points, goalDiff, goalsFor int
}

func naiveStats(matches []oracleMatch, teams []int) map[int]oracleStats {
	in := make(map[int]bool, len(teams))
	for _, team := range teams {
		in[team] = true
	}
	result := map[int]oracleStats{}
	for _, match := range matches {
		if !in[match.home] || !in[match.away] {
			continue
		}
		home := result[match.home]
		away := result[match.away]
		home.goalsFor += match.homeGoals
		away.goalsFor += match.awayGoals
		home.goalDiff += match.homeGoals - match.awayGoals
		away.goalDiff += match.awayGoals - match.homeGoals
		switch {
		case match.homeGoals > match.awayGoals:
			home.points += 3
		case match.homeGoals < match.awayGoals:
			away.points += 3
		default:
			home.points++
			away.points++
		}
		result[match.home] = home
		result[match.away] = away
	}
	return result
}

func naiveClassKey(stats oracleStats, mode classKeyMode) [3]int {
	switch mode {
	case classByPoints:
		return [3]int{stats.points}
	case classByGoals:
		return [3]int{stats.goalDiff, stats.goalsFor}
	default:
		return [3]int{stats.points, stats.goalDiff, stats.goalsFor}
	}
}

func naiveClasses(teams []int, stats map[int]oracleStats, mode classKeyMode) [][]int {
	sorted := append([]int(nil), teams...)
	sort.Slice(sorted, func(i, j int) bool {
		left := stats[sorted[i]]
		right := stats[sorted[j]]
		if mode != classByGoals && left.points != right.points {
			return left.points > right.points
		}
		if mode != classByPoints && left.goalDiff != right.goalDiff {
			return left.goalDiff > right.goalDiff
		}
		if mode != classByPoints && left.goalsFor != right.goalsFor {
			return left.goalsFor > right.goalsFor
		}
		return sorted[i] < sorted[j]
	})

	classes := [][]int{}
	for _, team := range sorted {
		key := naiveClassKey(stats[team], mode)
		if len(classes) == 0 || naiveClassKey(stats[classes[len(classes)-1][0]], mode) != key {
			classes = append(classes, []int{team})
		} else {
			classes[len(classes)-1] = append(classes[len(classes)-1], team)
		}
	}
	return classes
}

func naiveRankGroup(teams []int, matches []oracleMatch, global map[int]oracleStats) [][]int {
	if len(teams) < 2 {
		return [][]int{append([]int(nil), teams...)}
	}
	local := naiveStats(matches, teams)
	classes := naiveClasses(teams, local, classHeadToHead)
	if len(classes) == 1 {
		classes = naiveClasses(teams, global, classByGoals)
	}
	if len(classes) == 1 {
		tied := append([]int(nil), teams...)
		sort.Ints(tied)
		return [][]int{tied}
	}

	blocks := [][]int{}
	for _, class := range classes {
		blocks = append(blocks, naiveRankGroup(class, matches, global)...)
	}
	return blocks
}

func naiveTable(n int, matches []oracleMatch) []Row {
	teams := make([]int, n)
	for i := range teams {
		teams[i] = i + 1
	}
	global := naiveStats(matches, teams)
	pointGroups := naiveClasses(teams, global, classByPoints)
	rowsByTeam := map[int]Row{}
	position := 0
	for _, group := range pointGroups {
		for _, tiedTeams := range naiveRankGroup(group, matches, global) {
			rank := position + 1
			for _, team := range tiedTeams {
				position++
				rowsByTeam[team] = Row{
					Team:     team,
					Rank:     rank,
					Points:   global[team].points,
					GoalDiff: global[team].goalDiff,
					GoalsFor: global[team].goalsFor,
				}
			}
		}
	}

	rows := make([]Row, 0, n)
	for team := 1; team <= n; team++ {
		rows = append(rows, rowsByTeam[team])
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Rank != rows[j].Rank {
			return rows[i].Rank < rows[j].Rank
		}
		return rows[i].Team < rows[j].Team
	})
	return rows
}

func TestRandomMatchesAgainstNaiveOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))
	for iteration := 0; iteration < 2000; iteration++ {
		n := 2 + rng.Intn(5)
		pairs := make([][2]int, 0, n*(n-1))
		for home := 1; home <= n; home++ {
			for away := 1; away <= n; away++ {
				if home != away {
					pairs = append(pairs, [2]int{home, away})
				}
			}
		}
		rng.Shuffle(len(pairs), func(i, j int) { pairs[i], pairs[j] = pairs[j], pairs[i] })
		matches := make([]oracleMatch, 0, len(pairs))
		for _, pair := range pairs {
			if rng.Float64() < 0.28 {
				matches = append(matches, oracleMatch{
					home:      pair[0],
					away:      pair[1],
					homeGoals: rng.Intn(6),
					awayGoals: rng.Intn(6),
				})
			}
		}

		table, err := New(n)
		if err != nil {
			t.Fatal(err)
		}
		ordered := append([]oracleMatch(nil), matches...)
		sort.Slice(ordered, func(i, j int) bool {
			if ordered[i].home != ordered[j].home {
				return ordered[i].home < ordered[j].home
			}
			return ordered[i].away < ordered[j].away
		})
		for _, match := range ordered {
			mustAdd(t, table, match.home, match.away, match.homeGoals, match.awayGoals)
		}

		shuffled := append([]oracleMatch(nil), matches...)
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		shuffledTable, _ := New(n)
		for _, match := range shuffled {
			mustAdd(t, shuffledTable, match.home, match.away, match.homeGoals, match.awayGoals)
		}

		got := table.Table()
		orderIndependent := shuffledTable.Table()
		want := naiveTable(n, matches)
		basis := "initial point groups, then head-to-head-local class splits, with global goal fallback on one-class results"
		t.Logf("iteration %d n=%d\ninput=%v\noutput=%v\ndecision=%s", iteration, n, matches, got, basis)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("iteration %d table = %#v, want %#v", iteration, got, want)
		}
		if !reflect.DeepEqual(orderIndependent, want) {
			t.Fatalf("iteration %d shuffled order = %#v, want %#v", iteration, orderIndependent, want)
		}

		decisive, draws := 0, 0
		pointTotal := 0
		for _, match := range matches {
			if match.homeGoals == match.awayGoals {
				draws++
			} else {
				decisive++
			}
		}
		for _, row := range got {
			pointTotal += row.Points
		}
		if pointTotal != 3*decisive+2*draws {
			t.Fatalf("iteration %d points = %d, want %d", iteration, pointTotal, 3*decisive+2*draws)
		}
	}
}
