package gateway

import (
	"testing"
	"time"

	"ontology/digest"
	"ontology/record"
)

// TestExpiryBoundaryReexecutes pins the left-closed right-open rule:
// at now == created+ttl the record is already expired, a resubmit is a
// brand-new request and really executes again.
func TestExpiryBoundaryReexecutes(t *testing.T) {
	g, clock := newGateway()
	body := []byte("doc")
	if _, err := g.Submit("k", body, okExec("v1")); err != nil {
		t.Fatalf("submit err = %v", err)
	}
	clock.Advance(testTTL - time.Nanosecond)
	res, err := g.Submit("k", body, okExec("v2"))
	if err != nil || !res.Replayed || res.Value != "v1" {
		t.Fatalf("just before expiry must replay: res = %+v, err = %v", res, err)
	}
	if got := g.ExecCount(); got != 1 {
		t.Fatalf("ExecCount = %d, want 1", got)
	}
	clock.Advance(time.Nanosecond) // now == created + ttl exactly
	if st := g.Query("k"); st != (Status{}) {
		t.Fatalf("expired key status = %+v, want zero", st)
	}
	res, err = g.Submit("k", body, okExec("v2"))
	if err != nil || res.Replayed || res.Value != "v2" {
		t.Fatalf("at expiry must re-execute: res = %+v, err = %v", res, err)
	}
	if got := g.ExecCount(); got != 2 {
		t.Fatalf("ExecCount = %d, want 2", got)
	}
}

// TestExpiryDoesNotAffectInFlight advances the clock far beyond the
// TTL while the execution is still running: the record must stay
// visible to Query, and acquire must keep returning the in-flight
// record (so duplicates join it) instead of replacing it.
func TestExpiryDoesNotAffectInFlight(t *testing.T) {
	g, clock := newGateway()
	body := []byte("slow-doc")
	release := make(chan struct{})
	started := make(chan struct{})
	firstDone := make(chan Result, 1)
	go func() {
		res, _ := g.Submit("k", body, func() (any, error) {
			close(started)
			<-release
			return "slow-v", nil
		})
		firstDone <- res
	}()
	<-started
	clock.Advance(1000 * testTTL) // far past the TTL, still in-flight
	if st := g.Query("k"); !st.Exists || st.State != record.StateInFlight {
		t.Fatalf("in-flight past TTL status = %+v, want in-flight", st)
	}
	fp := digest.Of(body)
	rec, fresh := g.acquire("k", fp)
	if fresh {
		t.Fatal("acquire must not replace an in-flight record past TTL")
	}
	if !rec.MatchesBody(fp) {
		t.Fatal("in-flight record must still match its opening body")
	}
	close(release)
	if got := g.ExecCount(); got != 1 {
		t.Fatalf("ExecCount = %d, want 1", got)
	}
	if first := <-firstDone; first.Replayed || first.Value != "slow-v" {
		t.Fatalf("first submit res = %+v, want fresh slow-v", first)
	}
}

// TestExpiredKeyQueryIsZero ensures no residual state leaks through
// Query once a record has expired.
func TestExpiredKeyQueryIsZero(t *testing.T) {
	g, clock := newGateway()
	if _, err := g.Submit("k", []byte("a"), okExec("v")); err != nil {
		t.Fatalf("submit err = %v", err)
	}
	clock.Advance(testTTL)
	st := g.Query("k")
	if st.Exists || st.HasResult || st.Remaining != 0 {
		t.Fatalf("expired status = %+v, want zero", st)
	}
}
