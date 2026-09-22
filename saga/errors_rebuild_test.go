package saga

import (
	"errors"
	"testing"

	"ontology/journal"
	"ontology/step"
)

func TestDeterministicErrors(t *testing.T) {
	cases := []struct {
		name string
		want error
		not  []error
		run  func(o *Orchestrator) error
		cfg  Config
	}{
		{"empty steps", ErrNoSteps, []error{ErrDuplicateKey, ErrNilCompensate},
			func(o *Orchestrator) error { _, e := o.Run("x", nil); return e }, Config{}},
		{"duplicate keys", ErrDuplicateKey, []error{ErrNoSteps, ErrNilCompensate},
			func(o *Orchestrator) error {
				_, e := o.Run("x", []step.Step{{Key: "k"}, {Key: "k"}})
				return e
			}, Config{}},
		{"nil compensate after success", ErrNilCompensate,
			[]error{ErrNoSteps, ErrDuplicateKey},
			func(o *Orchestrator) error {
				_, e := o.Run("x", []step.Step{
					{Key: "a", Forward: func() error { return nil }},
					{Key: "b", Forward: func() error { return nil }},
					{Key: "c", Forward: func() error { return defFail() }}})
				return e
			}, Config{}},
		{"max steps", ErrMaxSteps, []error{ErrNoSteps, ErrJournalLimit},
			func(o *Orchestrator) error {
				_, e := o.Run("x", []step.Step{{Key: "a"}, {Key: "b"}})
				return e
			}, Config{MaxSteps: 1}},
		{"journal limit", ErrJournalLimit, []error{ErrMaxSteps},
			func(o *Orchestrator) error {
				c := newCounters("a", "b")
				_, e := o.Run("x", []step.Step{
					mkStep("a", c, nil, nil, false), mkStep("b", c, defFail(), nil, false)})
				return e
			}, Config{MaxJournalRecords: 1}},
		{"resume missing", ErrInstanceNotFound, []error{ErrInstanceRunning},
			func(o *Orchestrator) error { _, e := o.Resume("ghost"); return e }, Config{}},
		{"invalid retries", ErrMaxRetries, nil,
			func(o *Orchestrator) error {
				_, e := New(func() int64 { return 0 }, Config{MaxRetries: -1})
				return e
			}, Config{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run(newO(t, tc.cfg))
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want %v", err, tc.want)
			}
			for _, d := range tc.not {
				if errors.Is(err, d) {
					t.Fatalf("%v must be distinct from %v", err, d)
				}
			}
		})
	}
}

func TestJournalLimitAtomic(t *testing.T) {
	o := newO(t, Config{MaxJournalRecords: 1})
	c := newCounters("a", "b")
	_, err := o.Run("x", []step.Step{
		mkStep("a", c, nil, nil, false), mkStep("b", c, defFail(), nil, false)})
	if !errors.Is(err, ErrJournalLimit) || o.journal.Len() != 1 {
		t.Fatalf("err=%v len=%d: over-limit append must leave no partial record",
			err, o.journal.Len())
	}
}

func TestRebuildMatchesOnline(t *testing.T) {
	cases := []struct {
		name   string
		build  func(c *counters) []step.Step
		status Status
	}{
		{"success", func(c *counters) []step.Step {
			return []step.Step{mkStep("a", c, nil, nil, false), mkStep("b", c, nil, nil, false)}
		}, Succeeded},
		{"compensated", func(c *counters) []step.Step {
			return []step.Step{mkStep("a", c, nil, nil, false), mkStep("b", c, nil, nil, false),
				mkStep("c", c, defFail(), nil, false)}
		}, Compensated},
		{"compensation failure", func(c *counters) []step.Step {
			return []step.Step{mkStep("a", c, nil, errBoom, false),
				mkStep("b", c, defFail(), nil, false)}
		}, CompensateFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := newO(t, Config{})
			c := newCounters("a", "b", "c")
			steps := tc.build(c)
			if _, err := o.Run("s", steps); err != nil && tc.status != CompensateFailed {
				t.Fatal(err)
			}
			if err := o.SelfCheck("s"); err != nil {
				t.Fatalf("SelfCheck: %v", err)
			}
			online, _ := o.State("s")
			offline := Reconstruct("s", o.journal.Read("s"), len(steps))
			if online.Status != offline.Status || online.Status != tc.status ||
				online.Records != offline.Records ||
				!eqInts(online.Succeeded, offline.Succeeded) ||
				!eqInts(online.CompensateFailures, offline.CompensateFailures) {
				t.Fatalf("online=%+v offline=%+v", online, offline)
			}
		})
	}
}

func TestSequenceValidation(t *testing.T) {
	rec := func(d journal.Direction, r journal.Result, i int) journal.Record {
		return journal.Record{Direction: d, Result: r, StepIndex: i}
	}
	cases := []struct {
		name string
		recs []journal.Record
		bad  bool
	}{
		{"valid forward", []journal.Record{rec(journal.Forward, journal.Success, 0),
			rec(journal.Forward, journal.Success, 1)}, false},
		{"valid compensate", []journal.Record{rec(journal.Forward, journal.Success, 0),
			rec(journal.Forward, journal.Failure, 1),
			rec(journal.Compensate, journal.Success, 0)}, false},
		{"compensate before forward", []journal.Record{
			rec(journal.Compensate, journal.Success, 0)}, true},
		{"duplicate forward success", []journal.Record{
			rec(journal.Forward, journal.Success, 0), rec(journal.Forward, journal.Success, 0)}, true},
		{"forward after compensate", []journal.Record{
			rec(journal.Forward, journal.Success, 0),
			rec(journal.Compensate, journal.Failure, 0),
			rec(journal.Forward, journal.Success, 1)}, true},
		{"index out of range", []journal.Record{
			rec(journal.Forward, journal.Success, 2)}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRecordSequence(tc.recs, 2)
			if tc.bad != (err != nil) {
				t.Fatalf("err=%v bad=%v", err, tc.bad)
			}
		})
	}
}
