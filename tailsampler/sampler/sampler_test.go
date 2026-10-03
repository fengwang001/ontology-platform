package sampler

import (
	"testing"

	"ontology/tailsampler/policy"
)

func baseParams() Params {
	return Params{W: 10, Sc: 3, Nmax: 2, Td: 50, Cmax: 4, L: 100, P: 5000, Wb: 1000, Q: 1}
}

func mustNew(t *testing.T, p Params) *Sampler {
	t.Helper()
	s, err := New(p)
	if err != nil {
		t.Fatalf("New(%+v): %v", p, err)
	}
	return s
}

func mustIngest(t *testing.T, s *Sampler, now uint64, tid, sid string, dur uint64, isErr bool) []Decision {
	t.Helper()
	d, err := s.Ingest(now, tid, sid, dur, isErr)
	if err != nil {
		t.Fatalf("Ingest(now=%d trace=%s span=%s dur=%d err=%v): %v", now, tid, sid, dur, isErr, err)
	}
	t.Logf("Ingest(now=%d trace=%s span=%s dur=%d err=%v) -> %d decision(s)", now, tid, sid, dur, isErr, len(d))
	logDecisions(t, d)
	return d
}

func mustTick(t *testing.T, s *Sampler, now uint64) []Decision {
	t.Helper()
	d, err := s.Tick(now)
	if err != nil {
		t.Fatalf("Tick(%d): %v", now, err)
	}
	t.Logf("Tick(%d) -> %d decision(s)", now, len(d))
	logDecisions(t, d)
	return d
}

func logDecisions(t *testing.T, ds []Decision) {
	t.Helper()
	for _, d := range ds {
		t.Logf("  decision trace=%s keep=%v reason=%q spans=%d at=%d evicted=%v",
			d.TraceID, d.Keep, d.Reason, d.Spans, d.At, d.Evicted)
	}
}

func wantDecision(t *testing.T, d Decision, tid string, keep bool, reason string, spans int, at uint64, evicted bool) {
	t.Helper()
	if d.TraceID != tid || d.Keep != keep || d.Reason != reason || d.Spans != spans || d.At != at || d.Evicted != evicted {
		t.Fatalf("decision = %+v, want trace=%s keep=%v reason=%s spans=%d at=%d evicted=%v",
			d, tid, keep, reason, spans, at, evicted)
	}
}

// TestSpecExample replays the worked example from the specification.
func TestSpecExample(t *testing.T) {
	p := baseParams()
	p.H = func(id string) uint32 {
		if id == "t2" {
			return 100
		}
		return 9999
	}
	s := mustNew(t, p)
	mustIngest(t, s, 0, "t1", "a", 30, false)
	mustIngest(t, s, 4, "t1", "b", 120, false)
	mustIngest(t, s, 5, "t2", "c", 10, false)

	d := mustTick(t, s, 14) // t1: lastSeen 4 + W 10 = 14 <= 14
	if len(d) != 1 {
		t.Fatalf("Tick(14) decisions = %d, want 1", len(d))
	}
	wantDecision(t, d[0], "t1", true, policy.ReasonLatency, 2, 14, false)

	d = mustTick(t, s, 15) // t2 silent now; Prob candidate but u=1 >= Q=1
	if len(d) != 1 {
		t.Fatalf("Tick(15) decisions = %d, want 1", len(d))
	}
	wantDecision(t, d[0], "t2", false, policy.ReasonBudget, 1, 15, false)

	mustIngest(t, s, 20, "t1", "d", 5, false) // cache hit: 14+50 > 20
	if got := s.LateKept(); got != 1 {
		t.Fatalf("LateKept = %d, want 1", got)
	}
	mustIngest(t, s, 64, "t1", "e", 5, false) // cache expired: 14+50 <= 64
	if got := s.LateKept(); got != 1 {
		t.Fatalf("LateKept = %d, want 1 (expired cache must not hit)", got)
	}
	if len(s.buf) != 1 || s.buf["t1"].entry.Count() != 1 {
		t.Fatalf("t1 should be re-buffered with 1 span, buf=%v", s.buf)
	}
}

func TestSilenceExactlyW(t *testing.T) {
	p := baseParams()
	p.P = 0
	s := mustNew(t, p)
	mustIngest(t, s, 4, "t1", "a", 5, false)
	if d := mustTick(t, s, 13); len(d) != 0 {
		t.Fatalf("Tick(13) = %v, want none (4+10 > 13)", d)
	}
	d := mustTick(t, s, 14)
	if len(d) != 1 {
		t.Fatalf("Tick(14) decisions = %d, want 1", len(d))
	}
	wantDecision(t, d[0], "t1", false, policy.ReasonSampledOut, 1, 14, false)
}

func TestScTriggerImmediate(t *testing.T) {
	p := baseParams()
	p.Sc = 2
	s := mustNew(t, p)
	mustIngest(t, s, 3, "t1", "a", 5, false)
	d := mustIngest(t, s, 7, "t1", "b", 5, true)
	if len(d) != 1 {
		t.Fatalf("Sc-triggered decisions = %d, want 1", len(d))
	}
	wantDecision(t, d[0], "t1", true, policy.ReasonError, 2, 7, false)
	if len(s.buf) != 0 {
		t.Fatalf("buffer should be empty after decision, got %d", len(s.buf))
	}
}

func TestEvictionOrder(t *testing.T) {
	p := baseParams()
	p.P = 0
	p.W = 1000
	s := mustNew(t, p)
	mustIngest(t, s, 100, "x", "s1", 1, false)
	mustIngest(t, s, 100, "y", "s1", 1, false)
	d := mustIngest(t, s, 101, "z", "s1", 1, false) // buffer full: evict (100,"x")
	if len(d) != 1 {
		t.Fatalf("eviction decisions = %d, want 1", len(d))
	}
	wantDecision(t, d[0], "x", false, policy.ReasonSampledOut, 1, 101, true)
	if _, ok := s.buf["z"]; !ok {
		t.Fatal("z should be buffered after eviction")
	}
	if _, ok := s.buf["x"]; ok {
		t.Fatal("x should be evicted")
	}
}

func TestEvictionConsumesQuota(t *testing.T) {
	p := baseParams()
	p.Nmax, p.P, p.L, p.W = 1, 10000, 1e9, 10
	s := mustNew(t, p)
	mustIngest(t, s, 0, "x", "s1", 1, true)
	d := mustIngest(t, s, 1, "y", "s1", 1, false) // evicts x with Error, u=1
	if len(d) != 1 {
		t.Fatalf("eviction decisions = %d, want 1", len(d))
	}
	wantDecision(t, d[0], "x", true, policy.ReasonError, 1, 1, true)
	d = mustTick(t, s, 11) // y: Prob candidate, but u=1 >= Q=1
	if len(d) != 1 {
		t.Fatalf("Tick(11) decisions = %d, want 1", len(d))
	}
	wantDecision(t, d[0], "y", false, policy.ReasonBudget, 1, 11, false)
}
