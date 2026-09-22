package correlate

import (
	"errors"
	"testing"
	"time"
)

func TestOrphanClassesAreDistinctAndCounted(t *testing.T) {
	cor, clk := newTestCorrelator(2)

	// 1) ID never allocated.
	err := cor.Deliver(Token{ID: 9})
	if !errors.Is(err, ErrOrphanUnknown) {
		t.Fatalf("unknown err = %v", err)
	}

	// 2) ID finished and idle, not yet reused.
	tok, _ := cor.Issue(time.Minute)
	if err := cor.Deliver(tok); err != nil {
		t.Fatal(err)
	}
	if err := cor.Deliver(tok); !errors.Is(err, ErrOrphanIdle) {
		t.Fatalf("idle err = %v, want ErrOrphanIdle", err)
	}

	// 3) ID reused by a newer generation: late reply is stale.
	clk.advance(time.Second)
	newTok, _ := cor.Issue(time.Minute)
	if newTok.ID != tok.ID || newTok.Epoch != tok.Epoch+1 {
		t.Fatalf("expected reuse with new epoch: %+v", newTok)
	}
	if err := cor.Deliver(tok); !errors.Is(err, ErrOrphanStale) {
		t.Fatalf("stale err = %v, want ErrOrphanStale", err)
	}

	got := cor.Counters()
	if got.OrphanUnknown != 1 || got.OrphanIdle != 1 || got.OrphanStale != 1 {
		t.Fatalf("orphan counts wrong: %+v", got)
	}
	if got.Orphans() != 3 {
		t.Fatalf("orphans total = %d, want 3", got.Orphans())
	}
}

func TestCanceledReplyIsOrphanAndCancelGuards(t *testing.T) {
	cor, _ := newTestCorrelator(2)
	tok, _ := cor.Issue(time.Minute)

	if err := cor.Cancel(Token{ID: 7}); !errors.Is(err, ErrNotInFlight) {
		t.Fatalf("cancel unknown = %v", err)
	}
	if err := cor.Cancel(tok); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	// Canceling again is a no-op error with no state change.
	before := cor.Counters()
	if err := cor.Cancel(tok); !errors.Is(err, ErrNotInFlight) {
		t.Fatalf("double cancel = %v", err)
	}
	if cor.Counters() != before {
		t.Fatal("failed cancel must not change counters")
	}
	// Reply after cancel: idle orphan (slot was released for reuse).
	if err := cor.Deliver(tok); !errors.Is(err, ErrOrphanIdle) {
		t.Fatalf("reply after cancel err = %v, want ErrOrphanIdle", err)
	}
	if got := cor.Counters().Canceled; got != 1 {
		t.Fatalf("canceled = %d, want 1", got)
	}
}

func TestTimeoutBoundaryLeftClosed(t *testing.T) {
	cor, clk := newTestCorrelator(1)
	tok, _ := cor.Issue(10 * time.Second)

	clk.advance(9 * time.Second)
	if _, ok := cor.Lookup(tok.ID); !ok {
		t.Fatal("must still be waiting one instant before deadline")
	}
	clk.advance(time.Second) // now == deadline
	if _, ok := cor.Lookup(tok.ID); ok {
		t.Fatal("now == deadline must time out")
	}
	// Timed-out ID is reusable; its old reply is stale, not completing it.
	newTok, _ := cor.Issue(time.Minute)
	if newTok.ID != tok.ID {
		t.Fatalf("reuse id = %d, want %d", newTok.ID, tok.ID)
	}
	if err := cor.Deliver(tok); !errors.Is(err, ErrOrphanStale) {
		t.Fatalf("post-timeout reply = %v", err)
	}
}
