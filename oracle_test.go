package ontology

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

type naiveGame struct {
	home int
	away int
	hg   int
	ag   int
}

type naiveTotals struct {
	points   int
	goalDiff int
	goalsFor int
}

type naiveStanding struct {
	team     int
	rank     int
	points   int
	goalDiff int
	goalsFor int
}

func naiveTable(n int, games []naiveGame) []naiveStanding {
	allTotals := naiveAllTotals(n, games)
	teams := make([]int, n)
	for team := range teams {
		teams[team] = team + 1
	}
	pointGroups := naiveSplit(teams, func(team int) [3]int {
		return [3]int{allTotals[team].points, 0, 0}
	})

	blocks := make([][]int, 0, n)
	for _, group := range pointGroups {
		blocks = append(blocks, naiveRankGroup(games, group, allTotals)...)
	}

	ranks := make([]int, n+1)
	placed := 0
	for _, block := range blocks {
		rank := placed + 1
		for _, team := range block {
			ranks[team] = rank
		}
		placed += len(block)
	}

	orderedTeams := append([]int(nil), teams...)
	sort.Slice(orderedTeams, func(i, j int) bool {
		if ranks[orderedTeams[i]] != ranks[orderedTeams[j]] {
			return ranks[orderedTeams[i]] < ranks[orderedTeams[j]]
		}
		return orderedTeams[i] < orderedTeams[j]
	})

	table := make([]naiveStanding, 0, n)
	for _, team := range orderedTeams {
		total := allTotals[team]
		table = append(table, naiveStanding{
			team:     team,
			rank:     ranks[team],
			points:   total.points,
			goalDiff: total.goalDiff,
			goalsFor: total.goalsFor,
		})
	}
	return table
}

func naiveRankGroup(games []naiveGame, group []int, allTotals []naiveTotals) [][]int {
	if len(group) < 2 {
		return [][]int{group}
	}

	internalTotals := naiveInternalTotals(len(allTotals)-1, games, group)
	classes := naiveSplit(group, func(team int) [3]int {
		return [3]int{
			internalTotals[team].points,
			internalTotals[team].goalDiff,
			internalTotals[team].goalsFor,
		}
	})
	if len(classes) >= 2 {
		return naiveRankClasses(games, classes, allTotals)
	}

	classes = naiveSplit(group, func(team int) [3]int {
		return [3]int{
			allTotals[team].goalDiff,
			allTotals[team].goalsFor,
			0,
		}
	})
	if len(classes) >= 2 {
		return naiveRankClasses(games, classes, allTotals)
	}

	return [][]int{group}
}

func naiveRankClasses(games []naiveGame, classes [][]int, allTotals []naiveTotals) [][]int {
	blocks := make([][]int, 0)
	for _, class := range classes {
		if len(class) < 2 {
			blocks = append(blocks, class)
			continue
		}
		blocks = append(blocks, naiveRankGroup(games, class, allTotals)...)
	}
	return blocks
}

func naiveSplit(teams []int, key func(int) [3]int) [][]int {
	ordered := append([]int(nil), teams...)
	sort.Slice(ordered, func(i, j int) bool {
		left := key(ordered[i])
		right := key(ordered[j])
		for index := range left {
			if left[index] != right[index] {
				return left[index] > right[index]
			}
		}
		return ordered[i] < ordered[j]
	})

	classes := make([][]int, 0)
	for _, team := range ordered {
		teamKey := key(team)
		if len(classes) == 0 || key(classes[len(classes)-1][0]) != teamKey {
			classes = append(classes, []int{team})
			continue
		}
		last := len(classes) - 1
		classes[last] = append(classes[last], team)
	}
	return classes
}

func naiveAllTotals(n int, games []naiveGame) []naiveTotals {
	totals := make([]naiveTotals, n+1)
	for _, game := range games {
		naiveAddResult(&totals[game.home], &totals[game.away], game.hg, game.ag)
	}
	return totals
}

func naiveInternalTotals(n int, games []naiveGame, group []int) []naiveTotals {
	inGroup := make([]bool, n+1)
	for _, team := range group {
		inGroup[team] = true
	}
	totals := make([]naiveTotals, n+1)
	for _, game := range games {
		if inGroup[game.home] && inGroup[game.away] {
			naiveAddResult(&totals[game.home], &totals[game.away], game.hg, game.ag)
		}
	}
	return totals
}

func naiveAddResult(home, away *naiveTotals, hg, ag int) {
	home.goalsFor += hg
	away.goalsFor += ag
	home.goalDiff += hg - ag
	away.goalDiff += ag - hg
	switch {
	case hg > ag:
		home.points += 3
	case hg < ag:
		away.points += 3
	default:
		home.points++
		away.points++
	}
}

func TestRandomMatchesAgainstNaiveOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(1063))

	for caseNumber := 1; caseNumber <= 2000; caseNumber++ {
		n := 2 + rng.Intn(5)
		games := randomNaiveGames(rng, n)
		input := formatNaiveGames(games)
		wantRows := naiveTable(n, games)

		first := mustLeague(t, n)
		for _, game := range games {
			if err := first.Add(game.home, game.away, game.hg, game.ag); err != nil {
				t.Fatalf("case %d Add(%v): %v", caseNumber, game, err)
			}
		}

		shuffled := append([]naiveGame(nil), games...)
		rng.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		second := mustLeague(t, n)
		for _, game := range shuffled {
			if err := second.Add(game.home, game.away, game.hg, game.ag); err != nil {
				t.Fatalf("case %d shuffled Add(%v): %v", caseNumber, game, err)
			}
		}

		firstTable := first.Table()
		secondTable := second.Table()
		reason := fmt.Sprintf("case=%d n=%d input=%s oracle=%v actual=%v shuffled=%v criterion=直接递归甲/乙/丙逐字段相等且乱序重放相同",
			caseNumber, n, input, formatNaiveStandings(wantRows), formatStandings(firstTable), formatStandings(secondTable))
		assertNaiveTable(t, firstTable, wantRows, reason)
		assertStandings(t, secondTable, firstTable, reason+" (shuffled registration)")

		for _, standing := range firstTable {
			rank, err := first.RankOf(standing.Team)
			if err != nil || rank != standing.Rank {
				t.Fatalf("%s: RankOf(%d)=(%d,%v), want %d", reason, standing.Team, rank, err, standing.Rank)
			}
		}
		assertPointConservation(t, n, games, firstTable, reason)
		t.Log(reason)

		if len(games) == 0 {
			continue
		}
		keepCount := rng.Intn(len(games))
		retained := append([]naiveGame(nil), games[:keepCount]...)
		removed := append([]naiveGame(nil), games[keepCount:]...)
		rng.Shuffle(len(removed), func(i, j int) {
			removed[i], removed[j] = removed[j], removed[i]
		})
		for _, game := range removed {
			if err := first.Remove(game.home, game.away); err != nil {
				t.Fatalf("case %d Remove(%v): %v", caseNumber, game, err)
			}
		}

		retainedRows := naiveTable(n, retained)
		remainingTable := first.Table()
		retainedReason := fmt.Sprintf("case=%d n=%d remaining_input=%s oracle=%v actual=%v criterion=登记后撤销未保留比赛与从未登记等价",
			caseNumber, n, formatNaiveGames(retained), formatNaiveStandings(retainedRows), formatStandings(remainingTable))
		assertNaiveTable(t, remainingTable, retainedRows, retainedReason)
		assertPointConservation(t, n, retained, remainingTable, retainedReason)
		t.Log(retainedReason)
	}
}

func randomNaiveGames(rng *rand.Rand, n int) []naiveGame {
	games := make([]naiveGame, 0, n*(n-1))
	for home := 1; home <= n; home++ {
		for away := 1; away <= n; away++ {
			if home == away || rng.Float64() < 0.45 {
				continue
			}
			games = append(games, naiveGame{
				home: home,
				away: away,
				hg:   rng.Intn(4),
				ag:   rng.Intn(4),
			})
		}
	}
	return games
}

func assertNaiveTable(t *testing.T, got []Standing, want []naiveStanding, reason string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: row count = %d, want %d", reason, len(got), len(want))
	}
	for i := range want {
		if got[i].Team != want[i].team ||
			got[i].Rank != want[i].rank ||
			got[i].Points != want[i].points ||
			got[i].GoalDiff != want[i].goalDiff ||
			got[i].GoalsFor != want[i].goalsFor {
			t.Fatalf("%s: row %d = %+v, want team=%d rank=%d points=%d gd=%d gf=%d",
				reason, i, got[i], want[i].team, want[i].rank, want[i].points, want[i].goalDiff, want[i].goalsFor)
		}
	}
}

func assertPointConservation(t *testing.T, n int, games []naiveGame, table []Standing, reason string) {
	t.Helper()
	decisive, draws := 0, 0
	for _, game := range games {
		if game.hg == game.ag {
			draws++
		} else {
			decisive++
		}
	}
	wantPoints := 3*decisive + 2*draws
	gotPoints := 0
	for _, standing := range table {
		gotPoints += standing.Points
	}
	if gotPoints != wantPoints {
		t.Fatalf("%s: total points = %d, want 3*%d+2*%d=%d", reason, gotPoints, decisive, draws, wantPoints)
	}
}

func formatNaiveGames(games []naiveGame) string {
	if len(games) == 0 {
		return "[]"
	}
	result := "["
	for i, game := range games {
		if i > 0 {
			result += ","
		}
		result += fmt.Sprintf("(%d,%d,%d,%d)", game.home, game.away, game.hg, game.ag)
	}
	return result + "]"
}

func formatNaiveStandings(rows []naiveStanding) string {
	result := "["
	for i, row := range rows {
		if i > 0 {
			result += ","
		}
		result += fmt.Sprintf("(t%d,r=%d,p=%d,gd=%d,gf=%d)", row.team, row.rank, row.points, row.goalDiff, row.goalsFor)
	}
	return result + "]"
}

func formatStandings(rows []Standing) string {
	result := "["
	for i, row := range rows {
		if i > 0 {
			result += ","
		}
		result += fmt.Sprintf("(t%d,r=%d,p=%d,gd=%d,gf=%d)", row.Team, row.Rank, row.Points, row.GoalDiff, row.GoalsFor)
	}
	return result + "]"
}
