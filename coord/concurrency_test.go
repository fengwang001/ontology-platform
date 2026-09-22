package coord

import (
	"sync"
	"testing"
	"time"

	"ontology/participant"
	"ontology/vote"
)

// Concurrent drives, votes and queries must be race-free, produce the
// decision exactly once, and never leak intermediate states.
func TestConcurrentDriveDecidesOnce(t *testing.T) {
	clock := newFakeClock()
	txn := NewTxn(clock.now, time.Minute)
	const n = 8
	members := make([]*participant.Participant, 0, n)
	for i := 0; i < n; i++ {
		p := participant.New(string(rune('a'+i)), vote.BallotAgree)
		members = append(members, p)
		if err := txn.Register(p); err != nil {
			t.Fatal(err)
		}
	}

	const workers = 32
	decisions := make([]vote.Decision, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			decisions[w] = txn.Drive()
		}(w)
	}
	// Concurrent queries while drives are in flight.
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = txn.Query()
		}()
	}
	wg.Wait()

	for w := 1; w < workers; w++ {
		if decisions[w] != decisions[0] {
			t.Fatalf("divergent decisions: %+v vs %+v", decisions[0], decisions[w])
		}
	}
	if decisions[0].Verdict != vote.VerdictCommit {
		t.Fatalf("decision: %+v", decisions[0])
	}
	if err := txn.Replay(); err != nil {
		t.Fatalf("replay: %v", err)
	}
	for i, p := range members {
		if p.State() != participant.StateCommitted {
			t.Fatalf("member %d: state %v", i, p.State())
		}
		if p.CommitCount() != 1 {
			t.Fatalf("member %d: commit count %d, want exactly 1", i, p.CommitCount())
		}
	}
}

// Concurrent drives where one participant rejects: exactly one abort
// decision, everyone ends aborted, abort actions run exactly once.
func TestConcurrentDriveWithReject(t *testing.T) {
	clock := newFakeClock()
	txn := NewTxn(clock.now, time.Minute)
	yes := participant.New("yes", vote.BallotAgree)
	no := participant.New("no", vote.BallotReject)
	txn.Register(yes)
	txn.Register(no)

	const workers = 16
	decisions := make([]vote.Decision, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			decisions[w] = txn.Drive()
		}(w)
	}
	wg.Wait()
	for w := 1; w < workers; w++ {
		if decisions[w] != decisions[0] {
			t.Fatalf("divergent decisions: %+v vs %+v", decisions[0], decisions[w])
		}
	}
	if decisions[0].Verdict != vote.VerdictAbort || decisions[0].Reason != vote.ReasonRejected {
		t.Fatalf("decision: %+v", decisions[0])
	}
	if yes.State() != participant.StateAborted || no.State() != participant.StateAborted {
		t.Fatalf("states: yes=%v no=%v", yes.State(), no.State())
	}
	if yes.AbortCount() != 1 {
		t.Fatalf("yes abort count %d, want 1", yes.AbortCount())
	}
}
