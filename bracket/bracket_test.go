package bracket

import (
	"errors"
	"testing"
)

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func assertMatch(t *testing.T, got, want Match) {
	t.Helper()
	if got != want {
		t.Fatalf("match (%d,%d) = %+v, want %+v", got.Round, got.Index, got, want)
	}
}

func mustReport(t *testing.T, tour *Tournament, r, i, w int) {
	t.Helper()
	if err := tour.Report(r, i, w); err != nil {
		t.Fatalf("Report(%d,%d,%d): %v", r, i, w, err)
	}
}

func TestInvalidN(t *testing.T) {
	for _, n := range []int{0, 1, 65, 100, -4} {
		if _, err := New(n); !errors.Is(err, ErrInvalidN) {
			t.Fatalf("New(%d) err = %v, want ErrInvalidN", n, err)
		}
	}
}

func TestFoldedOrder(t *testing.T) {
	cases := map[int][]int{
		2:  {1, 2},
		4:  {1, 4, 2, 3},
		8:  {1, 8, 4, 5, 2, 7, 3, 6},
		16: {1, 16, 8, 9, 4, 13, 5, 12, 2, 15, 7, 10, 3, 14, 6, 11},
	}
	for b, want := range cases {
		if got := foldedOrder(b); !equalInts(got, want) {
			t.Fatalf("foldedOrder(%d) = %v, want %v", b, got, want)
		}
	}
}

func TestFirstRoundByesN2(t *testing.T) {
	tour, err := New(2)
	if err != nil {
		t.Fatal(err)
	}
	m := tour.Bracket()[0][0]
	if m.Left != 1 || m.Right != 2 || m.Type != Unresolved || m.Winner != 0 {
		t.Fatalf("N=2 match = %+v", m)
	}
}

func TestFirstRoundByesN3(t *testing.T) {
	// order(4) = [1,4,2,3]; seed 4 absent => seed 1 gets the bye.
	tour, _ := New(3)
	round := tour.Bracket()[0]
	if round[0] != (Match{Round: 1, Index: 1, Left: 1, Winner: 1, Type: Bye}) {
		t.Fatalf("match1 = %+v, want bye for 1", round[0])
	}
	if round[1] != (Match{Round: 1, Index: 2, Left: 2, Right: 3}) {
		t.Fatalf("match2 = %+v, want 2 vs 3", round[1])
	}
	if err := tour.Report(1, 1, 1); !errors.Is(err, ErrByeMatch) {
		t.Fatalf("report bye err = %v", err)
	}
	final := tour.Bracket()[1][0]
	if final.Left != 1 || final.Right != 0 {
		t.Fatalf("final slots = %+v", final)
	}
}

func TestFirstRoundN6(t *testing.T) {
	// order(8) = [1,8,4,5,2,7,3,6]: seeds 1 and 2 get byes.
	tour, _ := New(6)
	round := tour.Bracket()[0]
	want := []Match{
		{Round: 1, Index: 1, Left: 1, Winner: 1, Type: Bye},
		{Round: 1, Index: 2, Left: 4, Right: 5},
		{Round: 1, Index: 3, Left: 2, Winner: 2, Type: Bye},
		{Round: 1, Index: 4, Left: 3, Right: 6},
	}
	for i := range want {
		assertMatch(t, round[i], want[i])
	}
	semi := tour.Bracket()[1]
	if semi[0].Left != 1 || semi[0].Right != 0 {
		t.Fatalf("semi1 = %+v, want 1 vs TBD", semi[0])
	}
	if semi[1].Left != 2 || semi[1].Right != 0 {
		t.Fatalf("semi2 = %+v, want 2 vs TBD", semi[1])
	}
}

func TestFirstRoundN8(t *testing.T) {
	tour, _ := New(8)
	round := tour.Bracket()[0]
	want := []Match{
		{Round: 1, Index: 1, Left: 1, Right: 8},
		{Round: 1, Index: 2, Left: 4, Right: 5},
		{Round: 1, Index: 3, Left: 2, Right: 7},
		{Round: 1, Index: 4, Left: 3, Right: 6},
	}
	for i := range want {
		assertMatch(t, round[i], want[i])
	}
}

func TestFirstRoundN64(t *testing.T) {
	tour, _ := New(64)
	round := tour.Bracket()[0]
	if len(round) != 32 {
		t.Fatalf("matches = %d, want 32", len(round))
	}
	order := foldedOrder(64)
	for i, m := range round {
		if m.Left != order[2*i] || m.Right != order[2*i+1] || m.Type != Unresolved {
			t.Fatalf("match %d = %+v, want %d vs %d", i+1, m, order[2*i], order[2*i+1])
		}
	}
	if len(tour.Bracket()) != 6 {
		t.Fatalf("rounds = %d, want 6", len(tour.Bracket()))
	}
}

func TestFullReportToChampion(t *testing.T) {
	tour, _ := New(8)
	mustReport(t, tour, 1, 1, 1)
	mustReport(t, tour, 1, 2, 4)
	mustReport(t, tour, 1, 3, 2)
	mustReport(t, tour, 1, 4, 3)
	mustReport(t, tour, 2, 1, 1)
	mustReport(t, tour, 2, 2, 2)
	if err := tour.Report(3, 1, 2); err != nil {
		t.Fatal(err)
	}
	champ, ok := tour.Champion()
	if !ok || champ != 2 {
		t.Fatalf("champion = (%d,%v), want (2,true)", champ, ok)
	}
	if err := tour.Withdraw(1); !errors.Is(err, ErrEliminated) {
		t.Fatalf("withdraw loser err = %v, want ErrEliminated", err)
	}
}

func TestReportRejectionOrder(t *testing.T) {
	tour, _ := New(6)
	if err := tour.Report(9, 1, 1); !errors.Is(err, ErrMatchNotFound) {
		t.Fatalf("got %v", err)
	}
	if err := tour.Report(2, 9, 1); !errors.Is(err, ErrMatchNotFound) {
		t.Fatalf("got %v", err)
	}
	if err := tour.Report(1, 1, 1); !errors.Is(err, ErrByeMatch) {
		t.Fatalf("got %v", err)
	}
	if err := tour.Report(2, 1, 1); !errors.Is(err, ErrNotReady) {
		t.Fatalf("got %v", err)
	}
	if err := tour.Report(1, 2, 9); !errors.Is(err, ErrNotContestant) {
		t.Fatalf("got %v", err)
	}
	if err := tour.Report(1, 2, 4); err != nil {
		t.Fatal(err)
	}
	if err := tour.Report(1, 2, 5); !errors.Is(err, ErrAlreadyPlayed) {
		t.Fatalf("got %v", err)
	}
}

func TestCorrectNextRound(t *testing.T) {
	tour, _ := New(8)
	mustReport(t, tour, 1, 1, 1)
	mustReport(t, tour, 1, 2, 5)

	if err := tour.Correct(1, 3, 2); !errors.Is(err, ErrNoResult) {
		t.Fatalf("got %v", err)
	}
	if err := tour.Correct(1, 1, 4); !errors.Is(err, ErrNotContestant) {
		t.Fatalf("got %v", err)
	}
	if err := tour.Correct(1, 1, 1); !errors.Is(err, ErrAlreadyWinner) {
		t.Fatalf("got %v", err)
	}
	// Semi not played: correction succeeds and 8 takes the same left slot.
	if err := tour.Correct(1, 1, 8); err != nil {
		t.Fatal(err)
	}
	semi := tour.Bracket()[1][0]
	if semi.Left != 8 || semi.Right != 5 {
		t.Fatalf("semi = %+v, want 8 vs 5", semi)
	}
	m1 := tour.Bracket()[0][0]
	if m1.Winner != 8 || m1.Type != Manual {
		t.Fatalf("match1 = %+v", m1)
	}
	if err := tour.Withdraw(1); !errors.Is(err, ErrEliminated) {
		t.Fatalf("old winner 1 should be eliminated, got %v", err)
	}
	mustReport(t, tour, 2, 1, 8)
	// Once the next-round match has a result, correction is rejected.
	before := tour.Bracket()[0][1]
	if err := tour.Correct(1, 2, 4); !errors.Is(err, ErrNextHasResult) {
		t.Fatalf("got %v", err)
	}
	after := tour.Bracket()[0][1]
	if after != before {
		t.Fatalf("rejected correction changed state: %+v -> %+v", before, after)
	}
}

func TestCorrectFinal(t *testing.T) {
	tour, _ := New(2)
	mustReport(t, tour, 1, 1, 1)
	if champ, ok := tour.Champion(); !ok || champ != 1 {
		t.Fatalf("champ = %d,%v", champ, ok)
	}
	if err := tour.Correct(1, 1, 2); err != nil {
		t.Fatal(err)
	}
	if champ, ok := tour.Champion(); !ok || champ != 2 {
		t.Fatalf("champ after final correction = %d,%v", champ, ok)
	}
	m := tour.Bracket()[0][0]
	if m.Winner != 2 || m.Type != Manual {
		t.Fatalf("final = %+v", m)
	}
}

func TestWithdrawCascadeWhenOpponentUnknown(t *testing.T) {
	// N=6: seed 1 waits in semi1 via bye. Withdraw 1 before the 4/5 winner
	// is known; once that match reports, the winner advances by walkover and
	// the cascade continues into later rounds.
	tour, _ := New(6)
	if err := tour.Withdraw(1); err != nil {
		t.Fatal(err)
	}
	mustReport(t, tour, 1, 2, 4)
	semi1 := tour.Bracket()[1][0]
	if semi1.Winner != 4 || semi1.Type != Technical {
		t.Fatalf("semi1 = %+v, want technical win for 4", semi1)
	}
	// Other half: 3 beats 6; semi2 = 2 vs 3 stays unresolved.
	mustReport(t, tour, 1, 4, 3)
	semi2 := tour.Bracket()[1][1]
	if semi2.Type != Unresolved || semi2.Left != 2 || semi2.Right != 3 {
		t.Fatalf("semi2 = %+v", semi2)
	}
	// Withdraw 2 => 3 advances; final (4 vs 3) becomes ready, nobody is
	// withdrawn there, so the final waits for a manual report.
	if err := tour.Withdraw(2); err != nil {
		t.Fatal(err)
	}
	semi2 = tour.Bracket()[1][1]
	if semi2.Winner != 3 || semi2.Type != Technical {
		t.Fatalf("semi2 after withdraw = %+v", semi2)
	}
	final := tour.Bracket()[2][0]
	if final.Type != Unresolved || final.Left != 4 || final.Right != 3 {
		t.Fatalf("final = %+v", final)
	}
}

func TestCascadeChainsMultipleRounds(t *testing.T) {
	// N=3: seed 1 has a bye and waits in the final. Withdraw 1, then report
	// 2 beats 3: the final immediately becomes ready and 2 is champion by
	// technical decision.
	tour, _ := New(3)
	if err := tour.Withdraw(1); err != nil {
		t.Fatal(err)
	}
	mustReport(t, tour, 1, 2, 2)
	final := tour.Bracket()[1][0]
	if final.Winner != 2 || final.Type != Technical {
		t.Fatalf("final = %+v, want technical champion 2", final)
	}
	if champ, ok := tour.Champion(); !ok || champ != 2 {
		t.Fatalf("champ = %d,%v", champ, ok)
	}
}

func TestBothWithdrawnSmallerSeedAdvances(t *testing.T) {
	// White-box setup of the otherwise hard-to-reach "both sides withdrawn"
	// case: build an N=8 tournament, put withdrawn winners 5 and 4 into the
	// empty final slots via a real semi correction + walkover chain is not
	// possible serially, so directly construct the state and run cascade().
	tour, _ := New(8)
	tour.mu.Lock()
	tour.withdrawn[4] = true
	tour.withdrawn[5] = true
	final := &tour.matches[2][0]
	final.left = 5
	final.right = 4
	tour.cascade()
	tour.mu.Unlock()
	got := tour.Bracket()[2][0]
	if got.Winner != 4 || got.Type != Technical {
		t.Fatalf("final = %+v, want technical win for smaller seed 4", got)
	}
}

func TestTechnicalCannotBeCorrected(t *testing.T) {
	tour, _ := New(3)
	if err := tour.Withdraw(1); err != nil {
		t.Fatal(err)
	}
	mustReport(t, tour, 1, 2, 2)
	final := tour.Bracket()[1][0]
	if final.Type != Technical {
		t.Fatalf("final = %+v", final)
	}
	if err := tour.Correct(2, 1, 1); !errors.Is(err, ErrTechnicalMatch) {
		t.Fatalf("got %v, want ErrTechnicalMatch", err)
	}
}

func TestWithdrawRejections(t *testing.T) {
	tour, _ := New(4)
	if err := tour.Withdraw(0); !errors.Is(err, ErrSeedOutOfRange) {
		t.Fatalf("got %v", err)
	}
	if err := tour.Withdraw(5); !errors.Is(err, ErrSeedOutOfRange) {
		t.Fatalf("got %v", err)
	}
	if err := tour.Withdraw(4); err != nil {
		t.Fatal(err)
	}
	if err := tour.Withdraw(4); !errors.Is(err, ErrAlreadyOut) {
		t.Fatalf("got %v", err)
	}
	// Match1 = 1 vs absent: bye, 1 advances. Match2 = 2 vs 3; report 2.
	// The final is 1 vs 2; 4 was absent and never played.
	mustReport(t, tour, 1, 2, 2)
	// 3 lost the manual match -> eliminated.
	if err := tour.Withdraw(3); !errors.Is(err, ErrEliminated) {
		t.Fatalf("got %v", err)
	}
	// 2 is withdrawn? no. Report the final, then any withdrawal is rejected
	// because the champion is decided (checked after elimination checks).
	mustReport(t, tour, 2, 1, 2)
	if err := tour.Withdraw(1); !errors.Is(err, ErrEliminated) {
		t.Fatalf("1 lost the final: got %v, want ErrEliminated", err)
	}
	if err := tour.Withdraw(2); !errors.Is(err, ErrChampionCrowned) {
		t.Fatalf("champion withdraw: got %v, want ErrChampionCrowned", err)
	}
}

func TestRejectedOperationsChangeNothing(t *testing.T) {
	tour, _ := New(6)
	before := tour.Bracket()
	_ = tour.Report(9, 9, 1)
	_ = tour.Report(1, 1, 1)
	_ = tour.Report(2, 1, 1)
	_ = tour.Report(1, 2, 9)
	_ = tour.Correct(9, 1, 1)
	_ = tour.Correct(1, 1, 1) // bye
	_ = tour.Correct(2, 1, 1) // no result
	_ = tour.Withdraw(0)
	_ = tour.Withdraw(99)
	after := tour.Bracket()
	if !bracketsEqual(before, after) {
		t.Fatalf("rejected operations mutated the bracket:\nbefore=%+v\nafter =%+v", before, after)
	}
}

func bracketsEqual(a, b [][]Match) bool {
	if len(a) != len(b) {
		return false
	}
	for r := range a {
		if len(a[r]) != len(b[r]) {
			return false
		}
		for i := range a[r] {
			if a[r][i] != b[r][i] {
				return false
			}
		}
	}
	return true
}
