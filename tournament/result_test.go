package tournament

import (
	"errors"
	"sync"
	"testing"
)

func TestRecordScoresPointsGoalDiffAndPlayed(t *testing.T) {
	tournament, err := New(4, 1)
	if err != nil {
		t.Fatal(err)
	}

	if err := tournament.Record(1, 1, 4, 2, 1); err != nil {
		t.Fatalf("Record home win: %v", err)
	}
	if err := tournament.Record(1, 2, 3, 2, 2); err != nil {
		t.Fatalf("Record draw: %v", err)
	}

	assertStats(t, tournament, 1, 3, 1, 1)
	assertStats(t, tournament, 4, 0, -1, 1)
	assertStats(t, tournament, 2, 1, 0, 1)
	assertStats(t, tournament, 3, 1, 0, 1)

	totalPoints := 0
	totalGoalDiff := 0
	for team := 1; team <= 4; team++ {
		totalPoints += tournament.Points(team)
		totalGoalDiff += tournament.GoalDiff(team)
	}
	if totalPoints != 5 || totalGoalDiff != 0 {
		t.Fatalf("totals = points %d, goal diff %d; want 5 and 0", totalPoints, totalGoalDiff)
	}
	if pending := tournament.Pending(); pending != 4 {
		t.Fatalf("Pending = %d, want 4", pending)
	}
	t.Logf("input Record(1,1,4,2,1), Record(1,2,3,2,2); output points=%d goalDiffSum=%d pending=%d; 判定依据: 一胜3分一平各1分，双方净胜球抵消", totalPoints, totalGoalDiff, tournament.Pending())
}

func TestRecordValidationOrderAndStateUnchanged(t *testing.T) {
	tournament, err := New(4, 1)
	if err != nil {
		t.Fatal(err)
	}

	invalidCases := []struct {
		name              string
		round, home, away int
		hg, ag            int
		want              error
	}{
		{"round out of range", 4, 1, 2, 0, 0, ErrFixtureNotFound},
		{"reversed direction", 1, 4, 1, 0, 0, ErrFixtureNotFound},
		{"bye cannot record", 2, 4, 2, 0, 0, ErrFixtureNotFound},
		{"negative score before duplicate checks", 1, 1, 4, -1, 0, ErrInvalidScore},
		{"score above 100", 1, 1, 4, 101, 0, ErrInvalidScore},
	}

	for _, tc := range invalidCases {
		err := tournament.Record(tc.round, tc.home, tc.away, tc.hg, tc.ag)
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: error %v, want %v", tc.name, err, tc.want)
		}
		t.Logf("input Record(%d,%d,%d,%d,%d); output=%v; 判定依据: %s", tc.round, tc.home, tc.away, tc.hg, tc.ag, err, tc.name)
	}
	if pending := tournament.Pending(); pending != 6 {
		t.Fatalf("rejected records changed Pending to %d, want 6", pending)
	}

	if err := tournament.Record(1, 1, 4, 1, 0); err != nil {
		t.Fatal(err)
	}
	if err := tournament.Record(1, 1, 4, 2, 0); !errors.Is(err, ErrAlreadyRecorded) {
		t.Fatalf("duplicate error = %v, want %v", err, ErrAlreadyRecorded)
	}
	assertStats(t, tournament, 1, 3, 1, 1)
	assertStats(t, tournament, 4, 0, -1, 1)
}

func TestWithdrawConvertsPendingAndKeepsRecordedResults(t *testing.T) {
	tournament, err := New(4, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := tournament.Record(1, 1, 4, 2, 2); err != nil {
		t.Fatal(err)
	}
	if err := tournament.Record(2, 3, 1, 1, 0); err != nil {
		t.Fatal(err)
	}

	if err := tournament.Withdraw(1); err != nil {
		t.Fatalf("Withdraw(1): %v", err)
	}
	if err := tournament.Record(3, 3, 4, 0, 1); err != nil {
		t.Fatalf("record independent fixture after team 1 withdrew: %v", err)
	}

	if err := tournament.Withdraw(4); err != nil {
		t.Fatalf("Withdraw(4): %v", err)
	}
	if err := tournament.Withdraw(3); err != nil {
		t.Fatalf("Withdraw(3): %v", err)
	}

	assertStats(t, tournament, 1, 1, -4, 3)
	assertStats(t, tournament, 2, 9, 9, 3)
	assertStats(t, tournament, 3, 3, -3, 3)
	assertStats(t, tournament, 4, 4, -2, 3)
	if pending := tournament.Pending(); pending != 0 {
		t.Fatalf("Pending after withdraw = %d, want 0", pending)
	}
	if err := tournament.Record(3, 1, 2, 0, 0); !errors.Is(err, ErrAlreadyTechnical) {
		t.Fatalf("technical match record error = %v, want %v", err, ErrAlreadyTechnical)
	}
	if err := tournament.Withdraw(1); !errors.Is(err, ErrTeamAlreadyWithdrew) {
		t.Fatalf("repeat withdraw error = %v, want %v", err, ErrTeamAlreadyWithdrew)
	}
	if err := tournament.Withdraw(0); !errors.Is(err, ErrInvalidTeam) {
		t.Fatalf("withdraw team 0 error = %v, want %v", err, ErrInvalidTeam)
	}
	if err := tournament.Withdraw(5); !errors.Is(err, ErrInvalidTeam) {
		t.Fatalf("withdraw virtual/outside team error = %v, want %v", err, ErrInvalidTeam)
	}
	t.Logf("input Withdraw(1), Record(3,3,4,0,1), Withdraw(4), Withdraw(3); output team1={points:%d gd:%d played:%d} team4={points:%d gd:%d played:%d} pending=0; 判定依据: 已登记2-2/1-0/0-1保留，各队未赛场只判一次3-0", tournament.Points(1), tournament.GoalDiff(1), tournament.Played(1), tournament.Points(4), tournament.GoalDiff(4), tournament.Played(4))
}

func TestDoubleRoundReversesHomeAway(t *testing.T) {
	tournament, err := New(4, 2)
	if err != nil {
		t.Fatal(err)
	}
	for round := 1; round <= 3; round++ {
		first := tournament.Fixtures(round)
		second := tournament.Fixtures(round + 3)
		for index := range first {
			if first[index].Bye != second[index].Bye || first[index].Home != second[index].Away || first[index].Away != second[index].Home {
				t.Fatalf("round %d pair %d not reversed: %#v vs %#v", round, index, first[index], second[index])
			}
		}
		t.Logf("input Fixtures(%d), Fixtures(%d); output first=%v second=%v; 判定依据: 第二圈相同对阵但主客互换", round, round+3, first, second)
	}
}

func TestConcurrentOperationsAreSerialized(t *testing.T) {
	tournament, err := New(4, 1)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for team := 1; team <= 4; team++ {
		wg.Add(1)
		go func(team int) {
			defer wg.Done()
			_ = tournament.Withdraw(team)
		}(team)
	}
	for round := 1; round <= 3; round++ {
		for _, fixture := range tournament.Fixtures(round) {
			if fixture.Bye {
				continue
			}
			wg.Add(1)
			fixture := fixture
			go func() {
				defer wg.Done()
				_ = tournament.Record(fixture.Round, fixture.Home, fixture.Away, 1, 1)
			}()
		}
	}
	wg.Wait()

	if pending := tournament.Pending(); pending != 0 {
		t.Fatalf("Pending after concurrent completion = %d, want 0", pending)
	}
	points := 0
	games := 0
	for team := 1; team <= 4; team++ {
		points += tournament.Points(team)
		games += tournament.Played(team)
	}
	if games != 12 {
		t.Fatalf("total played entries = %d, want 12", games)
	}
	if points < 12 || points > 18 {
		t.Fatalf("total points = %d, want a serial-equivalent value between 12 and 18", points)
	}
	t.Logf("input concurrent Record/Withdraw; output games=%d points=%d pending=0; 判定依据: 等价某个串行顺序，k场平局贡献2k分，其余决胜场贡献3分，总分=%d", games, points, 18)
}

func TestDeterministicReplay(t *testing.T) {
	first := replayTournament(t)
	second := replayTournament(t)
	for round := 1; round <= 6; round++ {
		if !fixturesEqual(first.Fixtures(round), second.Fixtures(round)) {
			t.Fatalf("round %d differs on replay", round)
		}
	}
	for team := 1; team <= 4; team++ {
		if first.Points(team) != second.Points(team) || first.GoalDiff(team) != second.GoalDiff(team) || first.Played(team) != second.Played(team) {
			t.Fatalf("team %d stats differ on replay", team)
		}
	}
}

func TestInvalidConstruction(t *testing.T) {
	cases := []struct {
		teams, laps int
	}{
		{1, 1},
		{65, 1},
		{4, 0},
		{4, 3},
	}
	for _, tc := range cases {
		tournament, err := New(tc.teams, tc.laps)
		if !errors.Is(err, ErrInvalidConfig) || tournament != nil {
			t.Fatalf("New(%d,%d) = %#v,%v; want nil,%v", tc.teams, tc.laps, tournament, err, ErrInvalidConfig)
		}
		t.Logf("input New(teams=%d,laps=%d); output tournament=nil error=%v; 判定依据: N需在2..64且L为1或2", tc.teams, tc.laps, err)
	}
}

func replayTournament(t *testing.T) *Tournament {
	t.Helper()
	tournament, err := New(4, 2)
	if err != nil {
		t.Fatal(err)
	}
	_ = tournament.Record(1, 1, 4, 2, 1)
	_ = tournament.Record(2, 2, 4, 0, 0)
	if err := tournament.Withdraw(3); err != nil {
		t.Fatal(err)
	}
	return tournament
}

func assertStats(t *testing.T, tournament *Tournament, team, points, goalDiff, played int) {
	t.Helper()
	if tournament.Points(team) != points || tournament.GoalDiff(team) != goalDiff || tournament.Played(team) != played {
		t.Fatalf("team %d stats = points %d, goalDiff %d, played %d; want %d,%d,%d", team, tournament.Points(team), tournament.GoalDiff(team), tournament.Played(team), points, goalDiff, played)
	}
}
