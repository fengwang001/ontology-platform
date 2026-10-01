package ontology

import (
	"errors"
	"sync"
	"testing"
)

func TestRadiusBoundaryAndWiderNarrowerAcceptance(t *testing.T) {
	lobby := mustLobby(t, 10, 10, 100, 4)
	mustJoin(t, lobby, 1, 100, 0)
	mustJoin(t, lobby, 3, 0, 0)

	mustJoin(t, lobby, 2, 110, 10)
	mustJoin(t, lobby, 4, 11, 10)
	matches := mustTick(t, lobby, 10)
	if len(matches) != 1 || matches[0].A.ID != 1 || matches[0].B.ID != 2 || matches[0].RatingGap != 10 {
		t.Fatalf("gap equal to newly joined player's W0 radius must match the narrower side, got %#v", matches)
	}

	if queue := lobby.Queue(); len(queue) != 2 || queue[0].ID != 3 || queue[1].ID != 4 {
		t.Fatalf("wide old player and narrow new player must not match when gap is 11, queue=%#v", queue)
	}
}

func TestRadiusIsCappedAtMaximum(t *testing.T) {
	lobby := mustLobby(t, 5, 10, 20, 2)
	mustJoin(t, lobby, 1, 100, 0)
	mustJoin(t, lobby, 2, 121, 100)
	matches := mustTick(t, lobby, 100)
	if len(matches) != 0 {
		t.Fatalf("radius must be capped at Wmax, got %#v", matches)
	}
}

func TestOldestWaitingPlayerChoosesBeforeGloballyNearestPair(t *testing.T) {
	lobby := mustLobby(t, 5, 0, 5, 4)
	mustJoin(t, lobby, 1, 100, 0)
	mustJoin(t, lobby, 2, 105, 1)
	mustJoin(t, lobby, 3, 200, 2)
	mustJoin(t, lobby, 4, 201, 3)

	matches := mustTick(t, lobby, 4)
	if len(matches) != 2 {
		t.Fatalf("expected two matches, got %#v", matches)
	}
	if matches[0].A.ID != 1 || matches[0].B.ID != 2 {
		t.Fatalf("oldest player must choose first, got %#v", matches[0])
	}
	if matches[1].A.ID != 3 || matches[1].B.ID != 4 {
		t.Fatalf("remaining globally nearest pair must then match, got %#v", matches[1])
	}
}

func TestTieBreakersAndAlreadyMatchedCannotBeReused(t *testing.T) {
	lobby := mustLobby(t, 10, 0, 10, 4)
	mustJoin(t, lobby, 1, 100, 0)
	mustJoin(t, lobby, 3, 90, 2)
	mustJoin(t, lobby, 2, 110, 2)
	mustJoin(t, lobby, 4, 90, 2)

	matches := mustTick(t, lobby, 3)
	if len(matches) != 2 {
		t.Fatalf("expected two disjoint matches, got %#v", matches)
	}
	if matches[0].A.ID != 1 || matches[0].B.ID != 2 {
		t.Fatalf("equal gap must choose earlier joined, got %#v", matches[0])
	}
	if matches[1].A.ID != 3 || matches[1].B.ID != 4 {
		t.Fatalf("matched player must not be reused, got %#v", matches[1])
	}
}

func TestRejectedOperationsDoNotChangeState(t *testing.T) {
	lobby := mustLobby(t, 1, 0, 1, 2)
	mustJoin(t, lobby, 1, 100, 5)

	expectReject(t, lobby.Join(-1, 100, 6), ReasonInvalidID)
	expectReject(t, lobby.Join(2, 100, 4), ReasonClockWentBack)
	expectReject(t, lobby.Join(2, 5001, 6), ReasonInvalidRating)
	expectReject(t, lobby.Join(1, 100, 6), ReasonIDAlreadyExists)

	mustJoin(t, lobby, 2, 101, 6)
	expectReject(t, lobby.Join(3, 100, 7), ReasonQueueFull)

	if queue := lobby.Queue(); len(queue) != 2 || queue[0].ID != 1 || queue[1].ID != 2 {
		t.Fatalf("rejected joins must not change queue, got %#v", queue)
	}

	_, err := lobby.Tick(3)
	expectReject(t, err, ReasonClockWentBack)
	expectReject(t, lobby.Leave(9, 7), ReasonIDNotFound)
	mustTick(t, lobby, 7)
	expectReject(t, lobby.Leave(1, 8), ReasonAlreadyMatched)
	mustJoin(t, lobby, 3, 100, 8)
	mustLeave(t, lobby, 3, 9)
	expectReject(t, lobby.Leave(3, 10), ReasonAlreadyLeft)

	if queue := lobby.Queue(); len(queue) != 0 {
		t.Fatalf("expected empty queue after successful pair and leave, got %#v", queue)
	}
}

func TestDeterministicReplay(t *testing.T) {
	first := runDeterministicScript(t)
	second := runDeterministicScript(t)

	if len(first.matches) != len(second.matches) {
		t.Fatalf("match count changed on replay: %d vs %d", len(first.matches), len(second.matches))
	}
	for index := range first.matches {
		if first.matches[index] != second.matches[index] {
			t.Fatalf("match %d changed on replay: %#v vs %#v", index, first.matches[index], second.matches[index])
		}
	}
	if len(first.queue) != len(second.queue) {
		t.Fatalf("queue length changed on replay: %d vs %d", len(first.queue), len(second.queue))
	}
	for index := range first.queue {
		if first.queue[index] != second.queue[index] {
			t.Fatalf("queue position %d changed on replay: %#v vs %#v", index, first.queue[index], second.queue[index])
		}
	}
}

type deterministicResult struct {
	matches []Match
	queue   []Player
}

func runDeterministicScript(t *testing.T) deterministicResult {
	t.Helper()
	lobby := mustLobby(t, 8, 2, 30, 6)
	matches := make([]Match, 0)

	mustJoin(t, lobby, 1, 100, 0)
	mustJoin(t, lobby, 2, 109, 1)
	mustJoin(t, lobby, 3, 200, 2)
	mustJoin(t, lobby, 4, 205, 3)
	matches = append(matches, mustTick(t, lobby, 5)...)
	mustJoin(t, lobby, 5, 300, 6)
	mustLeave(t, lobby, 5, 7)
	expectReject(t, lobby.Join(1, 100, 8), ReasonIDAlreadyExists)
	mustJoin(t, lobby, 6, 202, 8)
	matches = append(matches, mustTick(t, lobby, 9)...)

	return deterministicResult{matches: matches, queue: lobby.Queue()}
}

func TestConcurrentOperationsAreSerializable(t *testing.T) {
	lobby := mustLobby(t, 10, 1, 30, 50)
	const players = 100

	joinDone := make(chan struct{})
	go func() {
		defer close(joinDone)
		var wait sync.WaitGroup
		for id := int64(1); id <= players; id++ {
			wait.Add(1)
			go func(id int64) {
				defer wait.Done()
				_ = lobby.Join(id, 100+(id%40), 10)
			}(id)
		}
		wait.Wait()
	}()
	<-joinDone

	var wait sync.WaitGroup
	matchCh := make(chan Match, players)
	for id := int64(1); id <= players; id++ {
		wait.Add(2)
		go func(id int64) {
			defer wait.Done()
			_ = lobby.Leave(id, 11)
		}(id)
		go func(id int64) {
			defer wait.Done()
			matches, _ := lobby.Tick(11)
			for _, match := range matches {
				select {
				case matchCh <- match:
				default:
					t.Errorf("match channel full")
				}
			}
		}(id)
	}

	for range 20 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			queue := lobby.Queue()
			if len(queue) > lobby.capacity {
				t.Errorf("queue exceeded capacity: %d", len(queue))
			}
		}()
	}
	wait.Wait()
	close(matchCh)

	seenInMatch := make(map[int64]bool)
	for match := range matchCh {
		if seenInMatch[match.A.ID] || seenInMatch[match.B.ID] {
			t.Fatalf("player belongs to more than one returned match: %#v", match)
		}
		seenInMatch[match.A.ID] = true
		seenInMatch[match.B.ID] = true
	}

	lobby.mu.Lock()
	defer lobby.mu.Unlock()
	if len(lobby.queue) > lobby.capacity {
		t.Fatalf("queue length %d exceeds capacity %d", len(lobby.queue), lobby.capacity)
	}
	for _, player := range lobby.queue {
		if lobby.known[player.ID] != stateQueued {
			t.Fatalf("queued player %d has state %d", player.ID, lobby.known[player.ID])
		}
		if seenInMatch[player.ID] {
			t.Fatalf("matched player %d remains queued", player.ID)
		}
	}
}

func TestConstructorValidation(t *testing.T) {
	tests := []struct {
		name     string
		w0       int64
		g        int64
		wmax     int64
		capacity int
		reason   RejectReason
	}{
		{"negative w0", -1, 0, 0, 2, ReasonInvalidW0},
		{"negative g", 0, -1, 0, 2, ReasonInvalidG},
		{"g too large", 0, 1_000_001, 0, 2, ReasonInvalidG},
		{"wmax below w0", 5, 0, 4, 2, ReasonInvalidWMax},
		{"w0 too large", 1_000_000_001, 0, 1_000_000_001, 2, ReasonInvalidW0},
		{"wmax too large", 0, 0, 1_000_000_001, 2, ReasonInvalidWMax},
		{"capacity below two", 0, 0, 0, 1, ReasonInvalidCapacity},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewLobby(tt.w0, tt.g, tt.wmax, tt.capacity)
			expectReject(t, err, tt.reason)
		})
	}
}

func mustLobby(t *testing.T, w0, growth, wmax int64, capacity int) *Lobby {
	t.Helper()
	lobby, err := NewLobby(w0, growth, wmax, capacity)
	if err != nil {
		t.Fatalf("NewLobby() error = %v", err)
	}
	return lobby
}

func mustJoin(t *testing.T, lobby *Lobby, id, rating, now int64) {
	t.Helper()
	if err := lobby.Join(id, rating, now); err != nil {
		t.Fatalf("Join(%d, %d, %d) error = %v", id, rating, now, err)
	}
}

func mustLeave(t *testing.T, lobby *Lobby, id, now int64) {
	t.Helper()
	if err := lobby.Leave(id, now); err != nil {
		t.Fatalf("Leave(%d, %d) error = %v", id, now, err)
	}
}

func mustTick(t *testing.T, lobby *Lobby, now int64) []Match {
	t.Helper()
	matches, err := lobby.Tick(now)
	if err != nil {
		t.Fatalf("Tick(%d) error = %v", now, err)
	}
	return matches
}

func expectReject(t *testing.T, err error, reason RejectReason) {
	t.Helper()
	var validationErr ValidationError
	if !errors.As(err, &validationErr) || validationErr.Reason != reason {
		t.Fatalf("expected rejection %s, got %v", reason, err)
	}
}
