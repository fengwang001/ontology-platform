package league

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func mustAdd(t *testing.T, table *Table, home, away, homeGoals, awayGoals int) {
	t.Helper()
	if err := table.Add(home, away, homeGoals, awayGoals); err != nil {
		t.Fatalf("Add(%d,%d,%d,%d): %v", home, away, homeGoals, awayGoals, err)
	}
}

func teamOrder(rows []Row) []int {
	teams := make([]int, len(rows))
	for i, row := range rows {
		teams[i] = row.Team
	}
	return teams
}

func ranks(rows []Row) []int {
	values := make([]int, len(rows))
	for i, row := range rows {
		values[i] = row.Rank
	}
	return values
}

func logTable(t *testing.T, name string, rows []Row, reason string) {
	t.Helper()
	t.Logf("%s\ninput result: %#v\noutput table: %#v\ndecision: %s", name, rows, rows, reason)
}

func TestNewAndValidation(t *testing.T) {
	if _, err := New(1); !errors.Is(err, ErrInvalidTeam) {
		t.Fatalf("New(1) error = %v, want %v", err, ErrInvalidTeam)
	}
	if _, err := New(33); !errors.Is(err, ErrInvalidTeam) {
		t.Fatalf("New(33) error = %v, want %v", err, ErrInvalidTeam)
	}

	table, err := New(3)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		err  error
		call func() error
	}{
		{name: "invalid home", err: ErrInvalidTeam, call: func() error { return table.Add(0, 1, 1, 0) }},
		{name: "invalid away", err: ErrInvalidTeam, call: func() error { return table.Add(1, 4, 1, 0) }},
		{name: "same team", err: ErrSameTeam, call: func() error { return table.Add(1, 1, 1, 0) }},
		{name: "negative score", err: ErrInvalidScore, call: func() error { return table.Add(1, 2, -1, 0) }},
		{name: "large score", err: ErrInvalidScore, call: func() error { return table.Add(1, 2, 1, 101) }},
		{name: "duplicate pair", err: ErrMatchExists, call: func() error { return table.Add(1, 2, 1, 0) }},
	}
	mustAdd(t, table, 1, 2, 0, 0)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); !errors.Is(err, tc.err) {
				t.Fatalf("error = %v, want %v", err, tc.err)
			}
		})
	}

	if err := table.Remove(0, 2); !errors.Is(err, ErrInvalidTeam) {
		t.Fatalf("Remove invalid team error = %v, want %v", err, ErrInvalidTeam)
	}
	if err := table.Remove(2, 1); !errors.Is(err, ErrMatchNotExist) {
		t.Fatalf("Remove missing match error = %v, want %v", err, ErrMatchNotExist)
	}

	rows := table.Table()
	logTable(t, "rejected operations leave only 1 vs 2 draw", rows, "all rejected calls returned before mutation")
	want := []Row{
		{Team: 1, Rank: 1, Points: 1, GoalDiff: 0, GoalsFor: 0},
		{Team: 2, Rank: 1, Points: 1, GoalDiff: 0, GoalsFor: 0},
		{Team: 3, Rank: 3, Points: 0, GoalDiff: 0, GoalsFor: 0},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("table = %#v, want %#v", rows, want)
	}
}

func TestTwoTeamTieDecidedByHeadToHead(t *testing.T) {
	table, _ := New(5)
	mustAdd(t, table, 1, 3, 3, 0)
	mustAdd(t, table, 2, 4, 3, 0)
	mustAdd(t, table, 2, 5, 3, 0)
	mustAdd(t, table, 1, 2, 2, 1)

	rows := table.Table()
	logTable(t, "two-team tie", rows, "teams 1 and 2 have 6 total points; their head-to-head match gives team 1 more mutual points")
	wantTeams := []int{1, 2, 3, 4, 5}
	if got := teamOrder(rows); !reflect.DeepEqual(got, wantTeams) {
		t.Fatalf("order = %v, want %v", got, wantTeams)
	}
}

func addMatches(t *testing.T, table *Table, matches ...[4]int) {
	t.Helper()
	for _, match := range matches {
		mustAdd(t, table, match[0], match[1], match[2], match[3])
	}
}

func TestThreeTeamCycleFallsToGlobalGoals(t *testing.T) {
	table, _ := New(3)
	addMatches(t, table,
		[4]int{1, 2, 1, 0},
		[4]int{2, 3, 1, 0},
		[4]int{3, 1, 1, 0},
	)

	rows := table.Table()
	logTable(t, "three-team cycle", rows, "head-to-head gives each 3 points, gd 0, goals 1; global fallback is also one class, so all tie")
	if got, want := teamOrder(rows), []int{1, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if got, want := ranks(rows), []int{1, 1, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ranks = %v, want %v", got, want)
	}
}

func TestHeadToHeadSplitUsesOnlyPairInternals(t *testing.T) {
	table, _ := New(6)
	addMatches(t, table,
		[4]int{2, 3, 1, 0},
		[4]int{3, 2, 0, 0},
		[4]int{1, 2, 1, 0},
		[4]int{3, 1, 1, 0},
		[4]int{1, 4, 1, 0},
		[4]int{2, 5, 1, 0},
		[4]int{3, 6, 3, 0},
	)

	rows := table.Table()
	logTable(t, "pair recomputation", rows, "head-to-head creates class {2,3} then class {1}; global goals prefer 3 over 2, but recursion uses only their internal match, where 2 beats 3")
	if got, want := teamOrder(rows), []int{2, 3, 1, 4, 5, 6}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestGlobalSplitRestartsHeadToHeadInRemainingClass(t *testing.T) {
	table, _ := New(8)
	addMatches(t, table,
		[4]int{1, 2, 3, 0},
		[4]int{2, 3, 1, 0},
		[4]int{3, 1, 1, 0},
		[4]int{1, 4, 1, 0},
		[4]int{4, 1, 3, 0},
		[4]int{2, 4, 3, 0},
		[4]int{4, 2, 3, 0},
		[4]int{3, 4, 3, 0},
		[4]int{4, 3, 1, 0},
		[4]int{1, 5, 1, 0},
		[4]int{2, 6, 1, 0},
		[4]int{3, 7, 1, 0},
		[4]int{4, 8, 1, 0},
	)

	rows := table.Table()
	logTable(t, "global split then recursive restart", rows, "the four-team head-to-head class is identical; global goals separate 4, then class {1,2,3} restarts at head-to-head and splits as 1,3,2 by mutual goal difference")
	if got, want := teamOrder(rows), []int{4, 1, 3, 2, 5, 6, 7, 8}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestNoHeadToHeadMatchesGoesDirectlyToGlobalGoals(t *testing.T) {
	table, _ := New(6)
	addMatches(t, table,
		[4]int{1, 5, 3, 0},
		[4]int{2, 5, 2, 0},
		[4]int{3, 6, 1, 0},
		[4]int{4, 6, 4, 0},
	)

	rows := table.Table()
	logTable(t, "empty head-to-head group", rows, "teams 1-4 have no mutual games; all head-to-head triples are zero, so global goal difference orders them")
	if got, want := teamOrder(rows), []int{4, 1, 2, 3, 5, 6}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestSharedRanksOneTwoTwoFour(t *testing.T) {
	table, _ := New(4)
	addMatches(t, table,
		[4]int{1, 4, 1, 0},
		[4]int{4, 1, 0, 1},
		[4]int{2, 4, 1, 0},
		[4]int{3, 4, 1, 0},
	)

	rows := table.Table()
	logTable(t, "shared ranks after both tie rules", rows, "teams 2 and 3 have no mutual match and identical global goals, so both tie-break stages leave them tied; team 4 takes rank 4")
	if got, want := ranks(rows), []int{1, 2, 2, 4}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ranks = %v, want %v", got, want)
	}
	if got, want := teamOrder(rows), []int{1, 2, 3, 4}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestHeadToHeadGoalDifferenceZeroWithDifferentPoints(t *testing.T) {
	table, _ := New(5)
	addMatches(t, table,
		[4]int{1, 2, 1, 0},
		[4]int{2, 3, 1, 0},
		[4]int{1, 3, 2, 0},
		[4]int{2, 4, 1, 0},
		[4]int{3, 4, 1, 0},
		[4]int{3, 5, 1, 0},
	)

	rows := table.Table()
	logTable(t, "zero mutual gd with different mutual points", rows, "all tied on 6 total points; mutual points split 1,2,3, and team 2 has mutual goal difference 0")
	local := scopeStats(3, table.matches, []int{1, 2, 3})
	if local[2].goalDiff != 0 || local[2].points != 3 {
		t.Fatalf("team 2 mutual stats = %+v, want 3 points and goal difference 0", local[2])
	}
	if local[1].points == local[2].points {
		t.Fatalf("teams 1 and 2 unexpectedly tied on mutual points: %d", local[1].points)
	}
	if got, want := teamOrder(rows)[:3], []int{1, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestRemoveChangesRankingAndIsEquivalentToAbsence(t *testing.T) {
	table, _ := New(2)
	mustAdd(t, table, 1, 2, 1, 0)
	before := table.Table()
	if got, want := ranks(before), []int{1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("before remove ranks = %v, want %v", got, want)
	}
	if err := table.Remove(1, 2); err != nil {
		t.Fatal(err)
	}

	after := table.Table()
	fresh, _ := New(2)
	withoutMatch := fresh.Table()
	logTable(t, "remove restores absent-match state", after, "removing the only decisive game leaves both teams on zero points")
	if !reflect.DeepEqual(after, withoutMatch) {
		t.Fatalf("after remove = %#v, fresh table = %#v", after, withoutMatch)
	}
	if got, want := ranks(after), []int{1, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("after remove ranks = %v, want %v", got, want)
	}
}

func TestConcurrentMutationsAndQueries(t *testing.T) {
	table, _ := New(8)
	pairs := make([][2]int, 0)
	for home := 1; home <= 8; home++ {
		for away := 1; away <= 8; away++ {
			if home != away && home+away <= 9 {
				pairs = append(pairs, [2]int{home, away})
			}
		}
	}

	var writers sync.WaitGroup
	for index, pair := range pairs {
		writers.Add(1)
		go func(index int, pair [2]int) {
			defer writers.Done()
			homeGoals := index % 4
			awayGoals := (index + 1) % 4
			if err := table.Add(pair[0], pair[1], homeGoals, awayGoals); err != nil {
				t.Errorf("concurrent Add(%v): %v", pair, err)
			}
		}(index, pair)
	}

	var readers sync.WaitGroup
	for i := 0; i < 16; i++ {
		readers.Add(1)
		go func(seed int) {
			defer readers.Done()
			for j := 0; j < 50; j++ {
				team := 1 + (seed+j)%8
				if _, err := table.RankOf(team); err != nil {
					t.Errorf("concurrent RankOf(%d): %v", team, err)
				}
				_ = table.Table()
			}
		}(i)
	}

	writers.Wait()
	readers.Wait()

	table.mu.RLock()
	count := len(table.matches)
	table.mu.RUnlock()
	if count != len(pairs) {
		t.Fatalf("registered %d matches, want %d", count, len(pairs))
	}
	rows := table.Table()
	logTable(t, "concurrent registrations and queries", rows, "unique ordered pairs have one linearizable successful Add each; repeated snapshots must stay internally sorted")
	for i := 1; i < len(rows); i++ {
		if rows[i-1].Rank > rows[i].Rank || rows[i-1].Rank == rows[i].Rank && rows[i-1].Team > rows[i].Team {
			t.Fatalf("table order is not sorted by rank then team: %#v", rows)
		}
	}

	rank, err := table.RankOf(1)
	if err != nil {
		t.Fatal(err)
	}
	foundTeamOne := false
	for _, row := range rows {
		if row.Team == 1 {
			foundTeamOne = true
			if rank != row.Rank {
				t.Fatalf("RankOf(1) = %d, table says %d", rank, row.Rank)
			}
		}
	}
	if !foundTeamOne {
		t.Fatal("table omitted team 1")
	}
	if _, err := table.RankOf(9); !errors.Is(err, ErrInvalidTeam) {
		t.Fatalf("RankOf(9) error = %v, want %v", err, ErrInvalidTeam)
	}
}

func TestSameOperationSequenceReplaysIdentically(t *testing.T) {
	operations := []struct {
		home, away, homeGoals, awayGoals int
	}{
		{1, 2, 2, 1},
		{2, 3, 1, 1},
		{3, 1, 3, 2},
		{2, 1, 0, 0},
	}

	build := func(removeAndReadd bool) []Row {
		table, _ := New(3)
		for _, operation := range operations {
			mustAdd(t, table, operation.home, operation.away, operation.homeGoals, operation.awayGoals)
		}
		if removeAndReadd {
			if err := table.Remove(2, 3); err != nil {
				t.Fatal(err)
			}
			mustAdd(t, table, 2, 3, 1, 1)
		}
		return table.Table()
	}

	first := build(false)
	second := build(false)
	readded := build(true)
	logTable(t, "deterministic replay", second, "the same Add sequence is rebuilt twice; a Remove followed by the same Add is compared with the uninterrupted sequence")
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("replay mismatch: first=%#v second=%#v", first, second)
	}
	if !reflect.DeepEqual(first, readded) {
		t.Fatalf("remove/readd mismatch: original=%#v readded=%#v", first, readded)
	}
}
