package orchestrate

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"ontology/journal"
	"ontology/policy"
	"ontology/step"
)

// flakyThenOK returns a step whose Run fails retryably r times, then
// succeeds; used to grow the journal without growing the graph.
func flakyThenOK(id string, r *int, fails int) step.Step {
	return step.Step{ID: id, Run: func(context.Context) error {
		if *r < fails {
			*r++
			return errors.New("transient")
		}
		return nil
	}}
}

func runFlakyChain(t *testing.T, fails int) []byte {
	t.Helper()
	g := buildGraph(t, []string{"a", "b"}, [][2]string{{"a", "b"}})
	r := 0
	steps := []step.Step{flakyThenOK("a", &r, fails), noop("b")}
	o, err := New(Config{
		Graph:  g,
		Steps:  steps,
		Policy: policy.Policy{MaxRetries: fails},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := o.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	return o.JournalBytes()
}

func TestReplayNodeVisitsIndependentOfTrailLength(t *testing.T) {
	small := runFlakyChain(t, 96)   // 100 records
	large := runFlakyChain(t, 9996) // ~10000 records
	visits := func(b []byte) (int, int) {
		o, torn, err := Recover(Config{
			Graph:  buildGraph(t, []string{"a", "b"}, [][2]string{{"a", "b"}}),
			Steps:  []step.Step{noop("a"), noop("b")},
			Policy: policy.Policy{},
		}, b)
		if err != nil || torn {
			t.Fatalf("err=%v torn=%v", err, torn)
		}
		return o.ReplayNodeVisits(), o.Snapshot().JournalLen
	}
	v100, l100 := visits(small)
	v10000, l10000 := visits(large)
	if l100 < 100 || l10000 < 10000 {
		t.Fatalf("trail lengths = %d, %d", l100, l10000)
	}
	if v100 != v10000 {
		t.Fatalf("node visits grow with trail length: L=%d -> %d, L=%d -> %d",
			l100, v100, l10000, v10000)
	}
	if v100 != 2 { // exactly one pass over the N=2 nodes
		t.Fatalf("node visits = %d, want 2", v100)
	}
}

func TestReplayIsIdempotent(t *testing.T) {
	bytes := runFlakyChain(t, 7)
	mk := func() *Orchestrator {
		o, torn, err := Recover(Config{
			Graph: buildGraph(t, []string{"a", "b"}, [][2]string{{"a", "b"}}),
			Steps: []step.Step{noop("a"), noop("b")},
		}, bytes)
		if err != nil || torn {
			t.Fatalf("err=%v torn=%v", err, torn)
		}
		return o
	}
	s1 := mk().Snapshot()
	s2 := mk().Snapshot()
	if !reflect.DeepEqual(s1, s2) {
		t.Fatalf("replays differ:\n%+v\n%+v", s1, s2)
	}
	if s1.Steps["a"].ExecCount != 8 { // 7 retries + 1 success
		t.Fatalf("exec count = %d, want 8", s1.Steps["a"].ExecCount)
	}
}

func TestTornTailDroppedThenRecovered(t *testing.T) {
	g := buildGraph(t, []string{"a", "b"}, [][2]string{{"a", "b"}})
	o, err := New(Config{Graph: g, Steps: []step.Step{noop("a"), noop("b")}})
	if err != nil {
		t.Fatal(err)
	}
	if err := o.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	full := o.JournalBytes()
	torn := full[:len(full)-3] // crash halfway through the last record

	reruns := 0
	steps := []step.Step{
		{ID: "a", Run: func(context.Context) error { reruns++; return nil }},
		{ID: "b", Run: func(context.Context) error { reruns++; return nil }},
	}
	o2, wasTorn, err := Recover(Config{Graph: g, Steps: steps}, torn)
	if err != nil {
		t.Fatal(err)
	}
	if !wasTorn {
		t.Fatal("torn tail not detected")
	}
	if o2.Snapshot().JournalLen != 3 { // 4th record was half-written
		t.Fatalf("journal len = %d, want 3", o2.Snapshot().JournalLen)
	}
	if err := o2.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if o2.Snapshot().Phase != PhaseCompleted {
		t.Fatalf("phase = %s", o2.Snapshot().Phase)
	}
	if reruns != 1 { // only the torn-away step b re-ran
		t.Fatalf("reruns = %d, want 1", reruns)
	}
}

func TestRecoveredJournalStaysAppendable(t *testing.T) {
	j := journal.New(0, nil)
	j.Append(journal.PhaseStart, "a", "")
	raw := j.Bytes()
	o, torn, err := Recover(Config{
		Graph: buildGraph(t, []string{"a"}, nil),
		Steps: []step.Step{noop("a")},
	}, raw)
	if err != nil || torn {
		t.Fatalf("err=%v torn=%v", err, torn)
	}
	if err := o.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := o.Snapshot().JournalLen; got != 3 { // old Start + new Start + Success
		t.Fatalf("journal len = %d, want 3", got)
	}
}
