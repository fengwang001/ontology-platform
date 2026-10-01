package swiss

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func registerPlayers(t *testing.T, tournament *Tournament, names ...string) {
	t.Helper()
	for index, name := range names {
		seed, err := tournament.Register(name)
		if err != nil {
			t.Fatalf("Register(%q): %v", name, err)
		}
		if seed != index+1 {
			t.Fatalf("Register(%q) seed = %d, want %d", name, seed, index+1)
		}
	}
}

func reportGame(t *testing.T, tournament *Tournament, a, b, result int) {
	t.Helper()
	if err := tournament.Report(a, b, result); err != nil {
		t.Fatalf("Report(%d, %d, %d): %v", a, b, result, err)
	}
}

func TestNewAndRegisterValidation(t *testing.T) {
	for _, rounds := range []int{0, -1, 21} {
		if _, err := New(rounds); !errors.Is(err, ErrInvalidRounds) {
			t.Fatalf("New(%d) error = %v, want ErrInvalidRounds", rounds, err)
		}
	}

	tournament, err := New(3)
	if err != nil {
		t.Fatal(err)
	}
	registerPlayers(t, tournament, "a", "b")

	if _, err := tournament.Register(""); !errors.Is(err, ErrEmptyName) {
		t.Fatalf("empty name error = %v, want ErrEmptyName", err)
	}
	if _, err := tournament.Register("a"); !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("duplicate name error = %v, want ErrDuplicateName", err)
	}

	pairs, bye, err := tournament.Pair()
	if err != nil {
		t.Fatal(err)
	}
	if want := []Pairing{{1, 2}}; !reflect.DeepEqual(pairs, want) || bye != -1 {
		t.Fatalf("Pair() = %#v, %d; want %#v, -1", pairs, bye, want)
	}

	_, err = tournament.Register("c")
	if !errors.Is(err, ErrRegistrationClosed) {
		t.Fatalf("Register after Pair error = %v, want ErrRegistrationClosed", err)
	}
	_, err = tournament.Register("")
	if !errors.Is(err, ErrRegistrationClosed) {
		t.Fatalf("Register empty after Pair error = %v, want ErrRegistrationClosed", err)
	}
	_, err = tournament.Register("a")
	if !errors.Is(err, ErrRegistrationClosed) {
		t.Fatalf("Register duplicate after Pair error = %v, want ErrRegistrationClosed", err)
	}
}

func TestOddByeSelectionScoreAndOpponentScore(t *testing.T) {
	tournament, _ := New(4)
	registerPlayers(t, tournament, "1", "2", "3")

	pairs, bye, err := tournament.Pair()
	if err != nil {
		t.Fatal(err)
	}
	if want := []Pairing{{1, 2}}; !reflect.DeepEqual(pairs, want) || bye != 3 {
		t.Fatalf("Pair() = %#v, %d; want %#v, 3", pairs, bye, want)
	}

	reportGame(t, tournament, 1, 2, 2)
	if got, want := tournament.Standings(), []Standing{
		{1, 2, 0},
		{3, 2, 0},
		{2, 0, 2},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Standings() = %#v, want %#v", got, want)
	}

	pairs, bye, err = tournament.Pair()
	if err != nil {
		t.Fatal(err)
	}
	if want := []Pairing{{1, 3}}; !reflect.DeepEqual(pairs, want) || bye != 2 {
		t.Fatalf("second Pair() = %#v, %d; want %#v, 2", pairs, bye, want)
	}
	reportGame(t, tournament, 3, 1, 2)

	if got, want := tournament.Standings(), []Standing{
		{3, 4, 2},
		{1, 2, 6},
		{2, 2, 2},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Standings() = %#v, want %#v", got, want)
	}
}

func TestPairUsesFirstLaterUnplayedOpponent(t *testing.T) {
	tournament, _ := New(3)
	registerPlayers(t, tournament, "1", "2", "3", "4")

	pairs, bye, err := tournament.Pair()
	if err != nil {
		t.Fatal(err)
	}
	if want := []Pairing{{1, 2}, {3, 4}}; !reflect.DeepEqual(pairs, want) || bye != -1 {
		t.Fatalf("first Pair() = %#v, %d", pairs, bye)
	}
	for _, pair := range pairs {
		reportGame(t, tournament, pair.X, pair.Y, 1)
	}

	pairs, _, err = tournament.Pair()
	if err != nil {
		t.Fatal(err)
	}
	if want := []Pairing{{1, 3}, {2, 4}}; !reflect.DeepEqual(pairs, want) {
		t.Fatalf("second Pair() = %#v, want %#v", pairs, want)
	}
}

func TestFourPlayersMeetEveryPairThenNoPairing(t *testing.T) {
	tournament, _ := New(4)
	registerPlayers(t, tournament, "1", "2", "3", "4")

	wantRounds := [][]Pairing{
		{{1, 2}, {3, 4}},
		{{1, 3}, {2, 4}},
		{{1, 4}, {2, 3}},
	}
	for round, want := range wantRounds {
		pairs, bye, err := tournament.Pair()
		if err != nil {
			t.Fatalf("round %d: %v", round+1, err)
		}
		if !reflect.DeepEqual(pairs, want) || bye != -1 {
			t.Fatalf("round %d = %#v, %d; want %#v, -1", round+1, pairs, bye, want)
		}
		for _, pair := range pairs {
			reportGame(t, tournament, pair.X, pair.Y, 1)
		}
	}

	if _, _, err := tournament.Pair(); !errors.Is(err, ErrNoLegalPairing) {
		t.Fatalf("fourth Pair error = %v, want ErrNoLegalPairing", err)
	}
}

func TestNonBacktrackingFailureReachableAndRollsBack(t *testing.T) {
	tournament, _ := New(4)
	registerPlayers(t, tournament, "1", "2", "3", "4", "5")

	rounds := []struct {
		pairs   []Pairing
		bye     int
		results []int
	}{
		{[]Pairing{{1, 2}, {3, 4}}, 5, []int{0, 0}},
		{[]Pairing{{2, 4}, {5, 1}}, 3, []int{1, 0}},
		{[]Pairing{{2, 3}, {4, 5}}, 1, []int{0, 1}},
	}
	for round, roundData := range rounds {
		pairs, bye, err := tournament.Pair()
		if err != nil {
			t.Fatalf("round %d: %v", round+1, err)
		}
		if !reflect.DeepEqual(pairs, roundData.pairs) || bye != roundData.bye {
			t.Fatalf("round %d = %#v, %d; want %#v, %d", round+1, pairs, bye, roundData.pairs, roundData.bye)
		}
		for index, pair := range pairs {
			reportGame(t, tournament, pair.X, pair.Y, roundData.results[index])
		}
	}

	beforeScores := append([]int(nil), tournament.scores...)
	beforeByes := append([]bool(nil), tournament.byes...)
	if _, _, err := tournament.Pair(); !errors.Is(err, ErrNoLegalPairing) {
		t.Fatalf("fourth Pair error = %v, want ErrNoLegalPairing", err)
	}
	if !reflect.DeepEqual(tournament.scores, beforeScores) || !reflect.DeepEqual(tournament.byes, beforeByes) {
		t.Fatal("failed Pair changed scores or bye records")
	}
	if tournament.round != 3 || tournament.roundActive || tournament.currentBye != -1 || tournament.currentPairs != nil {
		t.Fatalf("failed Pair left round state: round=%d active=%v bye=%d pairs=%#v",
			tournament.round, tournament.roundActive, tournament.currentBye, tournament.currentPairs)
	}
	if _, _, err := tournament.Pair(); !errors.Is(err, ErrNoLegalPairing) {
		t.Fatalf("repeated failed Pair error = %v, want ErrNoLegalPairing", err)
	}
}

func TestPairAndReportRejections(t *testing.T) {
	tournament, _ := New(1)
	registerPlayers(t, tournament, "1")
	if _, _, err := tournament.Pair(); !errors.Is(err, ErrNotEnoughPlayers) {
		t.Fatalf("Pair with one player error = %v", err)
	}

	tournament, _ = New(2)
	registerPlayers(t, tournament, "1", "2", "3", "4")

	if err := tournament.Report(1, 2, 1); !errors.Is(err, ErrNoRoundInProgress) {
		t.Fatalf("Report without round error = %v", err)
	}
	pairs, bye, err := tournament.Pair()
	if err != nil || !reflect.DeepEqual(pairs, []Pairing{{1, 2}, {3, 4}}) || bye != -1 {
		t.Fatalf("Pair() = %#v, %d, %v", pairs, bye, err)
	}

	if _, _, err := tournament.Pair(); !errors.Is(err, ErrRoundInProgress) {
		t.Fatalf("Pair during round error = %v, want ErrRoundInProgress", err)
	}
	if err := tournament.Report(1, 2, 3); !errors.Is(err, ErrInvalidResult) {
		t.Fatalf("invalid result error = %v, want ErrInvalidResult", err)
	}
	if err := tournament.Report(9, 9, 3); !errors.Is(err, ErrInvalidResult) {
		t.Fatalf("invalid result before membership error = %v, want ErrInvalidResult", err)
	}
	if err := tournament.Report(1, 1, 1); !errors.Is(err, ErrGameNotInRound) {
		t.Fatalf("same player report error = %v, want ErrGameNotInRound", err)
	}
	if err := tournament.Report(2, 1, 2); err != nil {
		t.Fatalf("reverse orientation Report: %v", err)
	}
	if err := tournament.Report(1, 2, 1); !errors.Is(err, ErrGameAlreadyReported) {
		t.Fatalf("duplicate report error = %v, want ErrGameAlreadyReported", err)
	}

	reportGame(t, tournament, 3, 4, 1)
	pairs, _, err = tournament.Pair()
	if err != nil {
		t.Fatal(err)
	}
	if want := []Pairing{{2, 3}, {4, 1}}; !reflect.DeepEqual(pairs, want) {
		t.Fatalf("second round pairs = %#v, want %#v", pairs, want)
	}
	reportGame(t, tournament, pairs[0].X, pairs[0].Y, 1)
	reportGame(t, tournament, pairs[1].X, pairs[1].Y, 1)
	if _, _, err := tournament.Pair(); !errors.Is(err, ErrTooManyRounds) {
		t.Fatalf("Pair after R rounds error = %v, want ErrTooManyRounds", err)
	}
	if err := tournament.Report(2, 3, 1); !errors.Is(err, ErrNoRoundInProgress) {
		t.Fatalf("Report after round error = %v, want ErrNoRoundInProgress", err)
	}
}

func TestReportRejectsUnknownSeedAndByePlayer(t *testing.T) {
	tournament, _ := New(2)
	registerPlayers(t, tournament, "1", "2", "3")
	pairs, bye, err := tournament.Pair()
	if err != nil || !reflect.DeepEqual(pairs, []Pairing{{1, 2}}) || bye != 3 {
		t.Fatalf("Pair() = %#v, %d, %v", pairs, bye, err)
	}
	if err := tournament.Report(1, 9, 1); !errors.Is(err, ErrGameNotInRound) {
		t.Fatalf("unknown seed report error = %v, want ErrGameNotInRound", err)
	}
	if err := tournament.Report(1, 3, 1); !errors.Is(err, ErrGameNotInRound) {
		t.Fatalf("bye player report error = %v, want ErrGameNotInRound", err)
	}
	reportGame(t, tournament, 1, 2, 1)
}

func TestConcurrentReadsAndReports(t *testing.T) {
	tournament, _ := New(1)
	registerPlayers(t, tournament, "1", "2", "3", "4")
	pairs, _, err := tournament.Pair()
	if err != nil {
		t.Fatal(err)
	}

	var waitGroup sync.WaitGroup
	start := make(chan struct{})
	for _, pair := range pairs {
		waitGroup.Add(1)
		go func(pair Pairing) {
			defer waitGroup.Done()
			<-start
			_ = tournament.Report(pair.X, pair.Y, 2)
			_ = tournament.Report(pair.X, pair.Y, 2)
		}(pair)
	}
	for range 8 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			_ = tournament.Standings()
		}()
	}
	close(start)
	waitGroup.Wait()

	reportedCount := 0
	scoreSum := 0
	for _, score := range tournament.scores {
		scoreSum += score
	}
	for _, yes := range tournament.reported {
		if yes {
			reportedCount++
		}
	}
	if reportedCount != len(pairs) || scoreSum != 2*len(pairs) || tournament.roundActive {
		t.Fatalf("after concurrent reports: reported=%d scoreSum=%d active=%v",
			reportedCount, scoreSum, tournament.roundActive)
	}
}
