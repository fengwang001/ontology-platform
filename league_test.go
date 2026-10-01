package ontology

import (
	"errors"
	"sync"
	"testing"
)

func TestNewLeagueRejectsInvalidSize(t *testing.T) {
	for _, n := range []int{-1, 0, 1, 33, 64} {
		league, err := NewLeague(n)
		if league != nil {
			t.Fatalf("NewLeague(%d) returned league %v, want nil", n, league)
		}
		if !errors.Is(err, ErrInvalidTeam) {
			t.Fatalf("NewLeague(%d) error = %v, want ErrInvalidTeam", n, err)
		}
	}
}

func TestAddValidationOrderAndNoStateChange(t *testing.T) {
	league := mustLeague(t, 3)

	tests := []struct {
		name string
		add  [4]int
		want error
	}{
		{name: "home out of range", add: [4]int{4, 1, 1, 0}, want: ErrInvalidTeam},
		{name: "away out of range", add: [4]int{1, 0, 1, 0}, want: ErrInvalidTeam},
		{name: "same team", add: [4]int{1, 1, 1, 0}, want: ErrSameTeam},
		{name: "negative home score", add: [4]int{1, 2, -1, 0}, want: ErrInvalidScore},
		{name: "large home score", add: [4]int{1, 2, 101, 0}, want: ErrInvalidScore},
		{name: "negative away score", add: [4]int{1, 2, 0, -1}, want: ErrInvalidScore},
		{name: "large away score", add: [4]int{1, 2, 0, 101}, want: ErrInvalidScore},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := league.Add(tt.add[0], tt.add[1], tt.add[2], tt.add[3])
			if !errors.Is(err, tt.want) {
				t.Fatalf("Add%v error = %v, want %v", tt.add, err, tt.want)
			}
		})
	}
	if err := league.Add(0, 0, -1, 101); !errors.Is(err, ErrInvalidTeam) {
		t.Fatalf("Add invalid team before same-team/score error = %v, want ErrInvalidTeam", err)
	}
	if err := league.Add(1, 1, -1, 101); !errors.Is(err, ErrSameTeam) {
		t.Fatalf("Add same team before score error = %v, want ErrSameTeam", err)
	}

	if err := league.Add(1, 2, 2, 1); err != nil {
		t.Fatalf("Add valid match: %v", err)
	}
	if err := league.Add(1, 2, -1, 101); !errors.Is(err, ErrInvalidScore) {
		t.Fatalf("Add duplicate invalid-score error = %v, want ErrInvalidScore", err)
	}
	if err := league.Add(1, 2, 3, 3); !errors.Is(err, ErrMatchAlreadyAdded) {
		t.Fatalf("Add duplicate error = %v, want ErrMatchAlreadyAdded", err)
	}

	got := league.Table()
	want := []Standing{
		{Team: 1, Rank: 1, Points: 3, GoalDiff: 1, GoalsFor: 2},
		{Team: 3, Rank: 2, Points: 0, GoalDiff: 0, GoalsFor: 0},
		{Team: 2, Rank: 3, Points: 0, GoalDiff: -1, GoalsFor: 1},
	}
	assertStandings(t, got, want, "rejected operations and duplicate must not change state")
}

func TestRemoveValidation(t *testing.T) {
	league := mustLeague(t, 2)

	if err := league.Remove(3, 1); !errors.Is(err, ErrInvalidTeam) {
		t.Fatalf("Remove invalid team error = %v, want ErrInvalidTeam", err)
	}
	if err := league.Remove(1, 2); !errors.Is(err, ErrMatchNotAdded) {
		t.Fatalf("Remove missing match error = %v, want ErrMatchNotAdded", err)
	}
	if err := league.Add(1, 2, 0, 0); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := league.Remove(1, 2); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := league.Remove(1, 2); !errors.Is(err, ErrMatchNotAdded) {
		t.Fatalf("second Remove error = %v, want ErrMatchNotAdded", err)
	}
}

func TestSameOperationSequenceReplaysIdentically(t *testing.T) {
	operations := [][4]int{
		{1, 3, 2, 1},
		{2, 3, 0, 0},
		{3, 1, 1, 1},
		{2, 1, 3, 2},
	}

	first := mustLeague(t, 3)
	second := mustLeague(t, 3)
	addGames(t, first, operations...)
	addGames(t, second, operations...)

	firstTable := first.Table()
	secondTable := second.Table()
	assertStandings(t, secondTable, firstTable, "same operation sequence must replay identically")

	if err := first.Remove(2, 3); err != nil {
		t.Fatalf("Remove replay: %v", err)
	}
	if err := second.Remove(2, 3); err != nil {
		t.Fatalf("Remove replay: %v", err)
	}
	assertStandings(t, second.Table(), first.Table(), "same remove sequence must replay identically")
	t.Logf("input=%v output=%v criterion=相同添加/撤销序列重放逐字段一致", operations, firstTable)
}

func TestTwoTeamsTieDecidedByHeadToHead(t *testing.T) {
	league := mustLeague(t, 2)
	addGames(t, league,
		[4]int{1, 2, 1, 1},
		[4]int{2, 1, 0, 1},
	)

	want := []Standing{
		{Team: 1, Rank: 1, Points: 4, GoalDiff: 1, GoalsFor: 2},
		{Team: 2, Rank: 2, Points: 1, GoalDiff: -1, GoalsFor: 1},
	}
	assertStandings(t, league.Table(), want, "head-to-head points split the tied teams")
	t.Logf("input=[(1,2,1,1),(2,1,0,1)] output=%v criterion=甲: 相互积分 4>1", want)
}

func TestThreeTeamCycleAllHeadToHeadKeysEqualFallsBackToGlobalGoals(t *testing.T) {
	league := mustLeague(t, 6)
	addGames(t, league,
		[4]int{1, 2, 1, 0},
		[4]int{2, 3, 1, 0},
		[4]int{3, 1, 1, 0},
		[4]int{1, 4, 3, 0},
		[4]int{5, 1, 1, 0},
		[4]int{2, 5, 2, 0},
		[4]int{6, 2, 1, 0},
		[4]int{3, 6, 1, 0},
		[4]int{4, 3, 1, 0},
	)

	want := []Standing{
		{Team: 1, Rank: 1, Points: 6, GoalDiff: 2, GoalsFor: 4},
		{Team: 2, Rank: 2, Points: 6, GoalDiff: 1, GoalsFor: 3},
		{Team: 3, Rank: 3, Points: 6, GoalDiff: 0, GoalsFor: 2},
		{Team: 6, Rank: 4, Points: 3, GoalDiff: 0, GoalsFor: 1},
		{Team: 5, Rank: 5, Points: 3, GoalDiff: -1, GoalsFor: 1},
		{Team: 4, Rank: 6, Points: 3, GoalDiff: -2, GoalsFor: 1},
	}
	assertStandings(t, league.Table(), want, "all mutual keys are (3,0,1), then global GD differs")
	t.Logf("input=[1/2/3各1:0循环;外部赛保持同分但总GD为2,1,0] output=%v criterion=甲一类后乙按总净胜球拆分", want)
}

func TestRecursivePairUsesOnlyItsInternalMatches(t *testing.T) {
	league := mustLeague(t, 4)
	addGames(t, league,
		[4]int{1, 2, 0, 1},
		[4]int{1, 3, 1, 0},
		[4]int{3, 1, 2, 2},
		[4]int{3, 2, 1, 0},
		[4]int{2, 3, 2, 2},
		[4]int{1, 4, 1, 1},
		[4]int{2, 4, 3, 3},
	)

	want := []Standing{
		{Team: 3, Rank: 1, Points: 5, GoalDiff: 0, GoalsFor: 5},
		{Team: 2, Rank: 2, Points: 5, GoalDiff: 0, GoalsFor: 6},
		{Team: 1, Rank: 3, Points: 5, GoalDiff: 0, GoalsFor: 4},
		{Team: 4, Rank: 4, Points: 2, GoalDiff: 0, GoalsFor: 4},
	}
	assertStandings(t, league.Table(), want, "甲 separates 3, then pair recurses on only match (1,2)")
	t.Logf("input=[甲:3为(5,0,5),1/2为(4,0,3);对子内部2胜1;全局进球2为6而1为4] output=%v criterion=递归对子只看 (1,2), 顺序与全局相反", want)
}

func TestGlobalSplitRestartsAtHeadToHeadForRemainingClass(t *testing.T) {
	league := mustLeague(t, 4)
	addGames(t, league,
		[4]int{1, 2, 1, 0},
		[4]int{2, 3, 1, 0},
		[4]int{3, 1, 1, 0},
		[4]int{1, 4, 4, 0},
		[4]int{2, 4, 1, 0},
		[4]int{3, 4, 1, 0},
	)

	want := []Standing{
		{Team: 1, Rank: 1, Points: 6, GoalDiff: 4, GoalsFor: 5},
		{Team: 2, Rank: 2, Points: 6, GoalDiff: 1, GoalsFor: 2},
		{Team: 3, Rank: 3, Points: 6, GoalDiff: 1, GoalsFor: 2},
		{Team: 4, Rank: 4, Points: 0, GoalDiff: -6, GoalsFor: 0},
	}
	assertStandings(t, league.Table(), want, "global GD separates singleton before restarting Rank for pair")
	t.Logf("input=[甲全员(3,0,1);乙:1为(4,5),2/3为(1,2);对子内部2胜3] output=%v criterion=乙拆分后对子重新从甲开始", want)
}

func TestNoInternalMatchesFallsDirectlyToGlobalGoals(t *testing.T) {
	league := mustLeague(t, 4)
	addGames(t, league,
		[4]int{1, 3, 3, 0},
		[4]int{4, 1, 1, 0},
		[4]int{2, 3, 1, 0},
		[4]int{4, 2, 1, 0},
	)
	want := []Standing{
		{Team: 4, Rank: 1, Points: 6, GoalDiff: 2, GoalsFor: 2},
		{Team: 1, Rank: 2, Points: 3, GoalDiff: 2, GoalsFor: 3},
		{Team: 2, Rank: 3, Points: 3, GoalDiff: 0, GoalsFor: 1},
		{Team: 3, Rank: 4, Points: 0, GoalDiff: -4, GoalsFor: 0},
	}
	assertStandings(t, league.Table(), want, "tied pair 1/2 has no internal match and is split by global GD")
	t.Logf("input=[1/2同分但互相无赛;总GD 1为2,2为0] output=%v criterion=甲一类后直接转乙拆分", want)
}

func TestRuleCSharesRanksOneTwoTwoFour(t *testing.T) {
	league := mustLeague(t, 5)
	addGames(t, league,
		[4]int{1, 2, 2, 0},
		[4]int{1, 3, 2, 0},
		[4]int{2, 5, 1, 0},
		[4]int{3, 5, 1, 0},
		[4]int{4, 5, 1, 1},
	)

	want := []Standing{
		{Team: 1, Rank: 1, Points: 6, GoalDiff: 4, GoalsFor: 4},
		{Team: 2, Rank: 2, Points: 3, GoalDiff: -1, GoalsFor: 1},
		{Team: 3, Rank: 2, Points: 3, GoalDiff: -1, GoalsFor: 1},
		{Team: 4, Rank: 4, Points: 1, GoalDiff: 0, GoalsFor: 1},
		{Team: 5, Rank: 5, Points: 1, GoalDiff: -2, GoalsFor: 1},
	}
	got := league.Table()
	assertStandings(t, got, want, "teams 2 and 3 tie after both 甲 and 乙")
	for _, team := range []int{2, 3} {
		rank, err := league.RankOf(team)
		if err != nil || rank != 2 {
			t.Fatalf("RankOf(%d) = (%d,%v), want (2,nil)", team, rank, err)
		}
	}
	t.Logf("input=[2/3都0:2负1且都1:0胜5, 互相无赛] output=%v criterion=2/3甲一类且乙相同，丙共享名次", got)
}

func TestMutualGoalDifferenceZeroButPointsDiffer(t *testing.T) {
	league := mustLeague(t, 4)
	addGames(t, league,
		[4]int{1, 3, 1, 0},
		[4]int{3, 1, 1, 0},
		[4]int{1, 2, 1, 1},
		[4]int{2, 3, 1, 1},
		[4]int{1, 4, 0, 0},
		[4]int{4, 1, 0, 0},
		[4]int{3, 4, 0, 0},
		[4]int{4, 3, 0, 0},
		[4]int{2, 4, 1, 0},
		[4]int{4, 2, 0, 0},
	)

	want := []Standing{
		{Team: 1, Rank: 1, Points: 6, GoalDiff: 0, GoalsFor: 2},
		{Team: 3, Rank: 1, Points: 6, GoalDiff: 0, GoalsFor: 2},
		{Team: 2, Rank: 3, Points: 6, GoalDiff: 1, GoalsFor: 3},
		{Team: 4, Rank: 4, Points: 5, GoalDiff: -1, GoalsFor: 0},
	}
	assertStandings(t, league.Table(), want, "mutual GD is zero for every tied team but mutual points differ")
	t.Logf("input=[相互积分1=3为4,2为2; 相互净胜球全0] output=%v criterion=甲先按相互积分拆分", want)
}

func TestRemoveChangesRanksAndRestoresState(t *testing.T) {
	league := mustLeague(t, 2)
	before := league.Table()
	addGames(t, league, [4]int{1, 2, 2, 1})

	want := []Standing{
		{Team: 1, Rank: 1, Points: 3, GoalDiff: 1, GoalsFor: 2},
		{Team: 2, Rank: 2, Points: 0, GoalDiff: -1, GoalsFor: 1},
	}
	assertStandings(t, league.Table(), want, "winner moves above loser")

	if err := league.Remove(1, 2); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	assertStandings(t, league.Table(), before, "removing the only match is equivalent to never adding it")
	t.Logf("input=add (1,2,2,1) then remove output_before=%v output_after=%v criterion=撤销后名次恢复", want, before)
}

func TestRankOfRejectsInvalidTeam(t *testing.T) {
	league := mustLeague(t, 2)
	for _, team := range []int{0, 3} {
		rank, err := league.RankOf(team)
		if rank != 0 || !errors.Is(err, ErrInvalidTeam) {
			t.Fatalf("RankOf(%d) = (%d,%v), want (0,ErrInvalidTeam)", team, rank, err)
		}
	}
}

func TestConcurrentOperationsAreSerializable(t *testing.T) {
	league := mustLeague(t, 4)
	const goroutines = 16
	const iterations = 100
	var wait sync.WaitGroup
	wait.Add(goroutines)

	for worker := 0; worker < goroutines; worker++ {
		go func(worker int) {
			defer wait.Done()
			for i := 0; i < iterations; i++ {
				home := worker%4 + 1
				away := (worker+1)%4 + 1
				if err := league.Add(home, away, i%3, (i+1)%3); err != nil &&
					!errors.Is(err, ErrMatchAlreadyAdded) {
					t.Errorf("concurrent Add(%d,%d): %v", home, away, err)
					return
				}
				_ = league.Table()
				if _, err := league.RankOf(home); err != nil {
					t.Errorf("concurrent RankOf(%d): %v", home, err)
					return
				}
				if err := league.Remove(home, away); err != nil &&
					!errors.Is(err, ErrMatchNotAdded) {
					t.Errorf("concurrent Remove(%d,%d): %v", home, away, err)
					return
				}
			}
		}(worker)
	}
	wait.Wait()

	want := newInitialTable(4)
	assertStandings(t, league.Table(), want, "all concurrent add/remove pairs leave no match")
	t.Logf("input=16 goroutines x100 repeated add/table/rank/remove output=%v criterion=末态与空联赛一致即存在串行交错", want)
}

func newInitialTable(n int) []Standing {
	table := make([]Standing, n)
	for team := range table {
		table[team] = Standing{Team: team + 1, Rank: 1}
	}
	return table
}

func mustLeague(t *testing.T, n int) *League {
	t.Helper()
	league, err := NewLeague(n)
	if err != nil {
		t.Fatalf("NewLeague(%d): %v", n, err)
	}
	return league
}

func addGames(t *testing.T, league *League, games ...[4]int) {
	t.Helper()
	for _, game := range games {
		if err := league.Add(game[0], game[1], game[2], game[3]); err != nil {
			t.Fatalf("Add(%v): %v", game, err)
		}
	}
}

func assertStandings(t *testing.T, got, want []Standing, reason string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got %d standings %v, want %d %v", reason, len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s: standing %d = %+v, want %+v\nall got %v\nall want %v", reason, i, got[i], want[i], got, want)
		}
	}
}
