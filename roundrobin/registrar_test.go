package roundrobin

import (
	"errors"
	"sync"
	"testing"
)

func TestConstructionRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		n   int
		l   int
		err error
	}{
		{1, 1, ErrInvalidN},
		{65, 1, ErrInvalidN},
		{4, 0, ErrInvalidL},
		{4, 3, ErrInvalidL},
	}
	for _, tc := range tests {
		_, err := NewRegistrar(tc.n, tc.l)
		t.Logf("input=NewRegistrar(%d,%d) output=%v decision=%v", tc.n, tc.l, err, tc.err)
		if !errors.Is(err, tc.err) {
			t.Fatalf("NewRegistrar(%d,%d) error = %v, want %v", tc.n, tc.l, err, tc.err)
		}
	}
}

func TestRecordScoresPointsGoalsAndPending(t *testing.T) {
	r, err := NewRegistrar(4, 1)
	if err != nil {
		t.Fatal(err)
	}

	if pending := r.Pending(); pending != 6 {
		t.Fatalf("initial pending = %d, want 6", pending)
	}
	if err := r.Record(1, 1, 4, 2, 1); err != nil {
		t.Fatalf("record winner: %v", err)
	}
	if err := r.Record(1, 2, 3, 2, 2); err != nil {
		t.Fatalf("record draw: %v", err)
	}

	assertStats(t, r, 1, 3, 1, 1)
	assertStats(t, r, 4, 0, -1, 1)
	assertStats(t, r, 2, 1, 0, 1)
	assertStats(t, r, 3, 1, 0, 1)
	if pending := r.Pending(); pending != 4 {
		t.Fatalf("pending after two records = %d, want 4", pending)
	}

	totalPoints := 0
	totalGoalDiff := 0
	for team := 1; team <= 4; team++ {
		totalPoints += r.Points(team)
		totalGoalDiff += r.GoalDiff(team)
	}
	t.Logf("input=N4 outputs points=%d goalDiff=%d decision=3 wins+2 draw and zero aggregate GD", totalPoints, totalGoalDiff)
	if totalPoints != 5 || totalGoalDiff != 0 {
		t.Fatalf("totals = points %d, GD %d; want 5, 0", totalPoints, totalGoalDiff)
	}
}

func TestRecordValidationOrderAndStateUnchanged(t *testing.T) {
	tests := []struct {
		name      string
		n         int
		round     int
		home      int
		away      int
		homeGoals int
		awayGoals int
		want      error
	}{
		{"round outside schedule", 4, 4, 1, 2, -1, 0, ErrInvalidFixture},
		{"reversed orientation", 4, 1, 4, 1, 0, 0, ErrInvalidFixture},
		{"bye fixture", 5, 1, 1, 6, 0, 0, ErrInvalidFixture},
		{"negative score", 4, 1, 2, 3, -1, 0, ErrInvalidScore},
		{"score over 100", 4, 1, 2, 3, 101, 0, ErrInvalidScore},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, err := NewRegistrar(tc.n, 1)
			if err != nil {
				t.Fatal(err)
			}
			err = r.Record(tc.round, tc.home, tc.away, tc.homeGoals, tc.awayGoals)
			t.Logf("input=%s output=%v decision=%s", tc.name, err, tc.want)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			assertStats(t, r, 1, 0, 0, 0)
			if pending := r.Pending(); pending != tc.n*(tc.n-1)/2 {
				t.Fatalf("pending after rejection = %d", pending)
			}
		})
	}

	r, _ := NewRegistrar(4, 1)
	if err := r.Record(1, 1, 4, 1, 0); err != nil {
		t.Fatal(err)
	}
	if err := r.Record(1, 1, 4, 0, 0); !errors.Is(err, ErrAlreadyRecorded) {
		t.Fatalf("duplicate error = %v, want ErrAlreadyRecorded", err)
	}
	assertStats(t, r, 1, 3, 1, 1)
	assertStats(t, r, 4, 0, -1, 1)
}

func TestWithdrawKeepsRecordedResultsAndDecidesPending(t *testing.T) {
	r, _ := NewRegistrar(5, 1)

	if err := r.Record(1, 2, 5, 4, 1); err != nil {
		t.Fatal(err)
	}
	beforePoints2 := r.Points(2)
	beforeGD2 := r.GoalDiff(2)
	beforePlayed2 := r.Played(2)

	if err := r.Withdraw(2); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	if r.Points(2) != beforePoints2 || r.GoalDiff(2) != beforeGD2-9 || r.Played(2) != beforePlayed2+3 {
		t.Fatalf("withdrawn team stats = %d/%d/%d, want %d/%d/%d", r.Points(2), r.GoalDiff(2), r.Played(2), beforePoints2, beforeGD2-9, beforePlayed2+3)
	}
	for _, team := range []int{1, 3, 4} {
		if got := r.Points(team); got != 3 {
			t.Fatalf("team %d points from technical win = %d, want 3", team, got)
		}
		if got := r.GoalDiff(team); got != 3 {
			t.Fatalf("team %d goal diff from technical win = %d, want 3", team, got)
		}
	}
	if pending := r.Pending(); pending != 6 {
		t.Fatalf("pending after withdrawal = %d, want 6", pending)
	}

	err := r.Record(5, 1, 2, 1, 1)
	t.Logf("input=record decided fixture output=%v decision=%v", err, ErrAlreadyDecided)
	if !errors.Is(err, ErrAlreadyDecided) {
		t.Fatalf("decided record error = %v, want ErrAlreadyDecided", err)
	}

	err = r.Record(5, 1, 2, -1, 101)
	t.Logf("input=record decided fixture with invalid scores output=%v decision=%v", err, ErrInvalidScore)
	if !errors.Is(err, ErrInvalidScore) {
		t.Fatalf("decided invalid-score error = %v, want ErrInvalidScore", err)
	}

	for _, tc := range []struct {
		team int
		err  error
	}{
		{0, ErrInvalidTeam},
		{6, ErrInvalidTeam},
		{2, ErrAlreadyWithdrawn},
	} {
		err := r.Withdraw(tc.team)
		t.Logf("input=Withdraw(%d) output=%v decision=%v", tc.team, err, tc.err)
		if !errors.Is(err, tc.err) {
			t.Fatalf("Withdraw(%d) error = %v, want %v", tc.team, err, tc.err)
		}
	}
}

func TestSecondLapReversesHomeAway(t *testing.T) {
	r, _ := NewRegistrar(4, 2)
	if err := r.Record(1, 1, 4, 2, 0); err != nil {
		t.Fatal(err)
	}
	if err := r.Record(4, 4, 1, 3, 1); err != nil {
		t.Fatal(err)
	}
	if err := r.Record(4, 1, 4, 0, 0); !errors.Is(err, ErrInvalidFixture) {
		t.Fatalf("first-lap orientation on second lap error = %v, want ErrInvalidFixture", err)
	}
	assertStats(t, r, 1, 3, 0, 2)
	assertStats(t, r, 4, 3, 0, 2)
	if pending := r.Pending(); pending != 10 {
		t.Fatalf("pending = %d, want 10", pending)
	}
}

func TestCompleteRoundRobinAggregateInvariants(t *testing.T) {
	r, _ := NewRegistrar(4, 1)
	scores := []struct {
		round, home, away, hg, ag int
	}{
		{1, 1, 4, 1, 0},
		{1, 2, 3, 2, 2},
		{2, 3, 1, 0, 2},
		{2, 2, 4, 3, 1},
		{3, 1, 2, 1, 1},
		{3, 3, 4, 0, 1},
	}
	for _, score := range scores {
		if err := r.Record(score.round, score.home, score.away, score.hg, score.ag); err != nil {
			t.Fatalf("record %+v: %v", score, err)
		}
	}

	totalPoints := 0
	totalGoalDiff := 0
	totalPlayed := 0
	for team := 1; team <= 4; team++ {
		totalPoints += r.Points(team)
		totalGoalDiff += r.GoalDiff(team)
		totalPlayed += r.Played(team)
	}
	t.Logf("input=complete N4 outputs points=%d goalDiff=%d played=%d pending=%d decision=4 decisive games*3+2 draws*2", totalPoints, totalGoalDiff, totalPlayed, r.Pending())
	if totalPoints != 16 || totalGoalDiff != 0 || totalPlayed != 12 || r.Pending() != 0 {
		t.Fatalf("aggregates = points %d GD %d played %d pending %d; want 16/0/12/0", totalPoints, totalGoalDiff, totalPlayed, r.Pending())
	}
}

func TestSecondWithdrawSkipsAlreadyDecidedFixtures(t *testing.T) {
	r, _ := NewRegistrar(4, 1)
	if err := r.Withdraw(1); err != nil {
		t.Fatal(err)
	}
	if err := r.Withdraw(2); err != nil {
		t.Fatal(err)
	}

	assertStats(t, r, 1, 0, -9, 3)
	assertStats(t, r, 2, 3, -3, 3)
	assertStats(t, r, 3, 6, 6, 2)
	assertStats(t, r, 4, 6, 6, 2)
	if pending := r.Pending(); pending != 1 {
		t.Fatalf("pending after two withdrawals = %d, want 1", pending)
	}
	t.Logf("input=Withdraw(1),Withdraw(2) output=pending1 decision=fixture between withdrawn teams decided once")
}

func TestReplayDeterministic(t *testing.T) {
	first, _ := NewRegistrar(5, 2)
	second, _ := NewRegistrar(5, 2)
	operations := []func(*Registrar) error{
		func(r *Registrar) error { return r.Record(1, 2, 5, 3, 1) },
		func(r *Registrar) error { return r.Record(4, 3, 1, 0, 0) },
		func(r *Registrar) error { return r.Withdraw(4) },
	}

	for _, operation := range operations {
		if err := operation(first); err != nil {
			t.Fatal(err)
		}
	}
	for _, operation := range operations {
		if err := operation(second); err != nil {
			t.Fatal(err)
		}
	}

	for round := 1; round <= 10; round++ {
		if !equalFixtures(first.Fixtures(round), second.Fixtures(round)) {
			t.Fatalf("round %d fixtures differ on replay", round)
		}
	}
	for team := 1; team <= 5; team++ {
		if first.Points(team) != second.Points(team) || first.GoalDiff(team) != second.GoalDiff(team) || first.Played(team) != second.Played(team) {
			t.Fatalf("team %d stats differ on replay", team)
		}
	}
	if first.Pending() != second.Pending() {
		t.Fatalf("pending differs on replay")
	}
	t.Logf("input=same 5-team double-lap operation sequence output=identical fixtures stats pending decision=deterministic replay")
}

func TestConcurrentOperationsAreSerializable(t *testing.T) {
	r, _ := NewRegistrar(6, 1)
	records := []struct {
		round, home, away, hg, ag int
	}{
		{1, 1, 6, 1, 0},
		{1, 2, 5, 0, 0},
		{1, 4, 3, 2, 2},
		{2, 5, 1, 1, 2},
		{2, 4, 6, 0, 3},
		{2, 2, 3, 2, 1},
	}

	var wg sync.WaitGroup
	for _, fixture := range records {
		fixture := fixture
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r.Record(fixture.round, fixture.home, fixture.away, fixture.hg, fixture.ag); err != nil {
				t.Errorf("concurrent Record: %v", err)
			}
		}()
	}
	for team := 1; team <= 6; team++ {
		team := team
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.Points(team)
			_ = r.GoalDiff(team)
			_ = r.Played(team)
			_ = r.Pending()
		}()
	}
	wg.Wait()

	if pending := r.Pending(); pending != 9 {
		t.Fatalf("pending after concurrent records = %d, want 9", pending)
	}
	totalPlayed := 0
	for team := 1; team <= 6; team++ {
		totalPlayed += r.Played(team)
	}
	if totalPlayed != 12 {
		t.Fatalf("total played = %d, want 12", totalPlayed)
	}
	t.Logf("input=6 concurrent records and concurrent queries output=pending9 played12 decision=mutex serialization")
}

func assertStats(t *testing.T, r *Registrar, team, points, goalDiff, played int) {
	t.Helper()
	if r.Points(team) != points || r.GoalDiff(team) != goalDiff || r.Played(team) != played {
		t.Fatalf("team %d stats = points %d GD %d played %d, want %d/%d/%d", team, r.Points(team), r.GoalDiff(team), r.Played(team), points, goalDiff, played)
	}
}

func equalFixtures(left, right []Fixture) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
