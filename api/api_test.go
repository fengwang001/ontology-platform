package api_test

import (
	"errors"
	"fmt"
	"testing"

	"ontology/api"
	"ontology/mrg"
	"ontology/seg"
)

func TestSelfCheck(t *testing.T) {
	if err := api.New().SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// TestBatchConsistency pins invariant 1.
func TestBatchConsistency(t *testing.T) {
	for _, n := range []int{1, 2, 5, 9, 18} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			a := api.New()
			for i := n; i >= 1; i-- { // reverse arrival order
				if err := a.Append(api.Event{Sid: "s", Seq: i, Value: (i*7 + 3) % 10}); err != nil {
					t.Fatalf("append: %v", err)
				}
			}
			want := 0
			for i := 1; i <= n; i++ {
				want = want*10 + (i*7+3)%10
			}
			if err := a.Close("s", n); err != nil {
				t.Fatalf("close: %v", err)
			}
			if r, closed, _ := a.Result("s"); !closed || r != want {
				t.Fatalf("got %d,%v want %d,true", r, closed, want)
			}
		})
	}
}

// TestResultImmutability pins invariant 2.
func TestResultImmutability(t *testing.T) {
	a := api.New()
	a.Append(api.Event{Sid: "s", Seq: 1, Value: 5})
	a.Append(api.Event{Sid: "s", Seq: 2, Value: 2})
	if err := a.Close("s", 2); err != nil {
		t.Fatalf("close: %v", err)
	}
	ops := []struct {
		name    string
		run     func() error
		wantErr bool
	}{
		{"replay same", func() error { return a.Append(api.Event{Sid: "s", Seq: 1, Value: 5}) }, true},
		{"conflict", func() error { return a.Append(api.Event{Sid: "s", Seq: 1, Value: 9}) }, true},
		{"append new seq", func() error { return a.Append(api.Event{Sid: "s", Seq: 3, Value: 9}) }, true},
		{"close different N", func() error { return a.Close("s", 3) }, true},
		{"close same N", func() error { return a.Close("s", 2) }, false},
	}
	for _, o := range ops {
		if err := o.run(); (err != nil) != o.wantErr {
			t.Fatalf("%s: err=%v", o.name, err)
		}
		if r, closed, _ := a.Result("s"); !closed || r != 52 {
			t.Fatalf("after %s: frozen result changed to %d,%v", o.name, r, closed)
		}
	}
}

// TestFailureNoTrace pins invariant 4.
func TestFailureNoTrace(t *testing.T) {
	cases := []struct {
		name string
		ev   api.Event
		want error
	}{
		{"empty sid", api.Event{Sid: "", Seq: 1, Value: 1}, mrg.ErrEmptySid},
		{"zero seq", api.Event{Sid: "s", Seq: 0, Value: 1}, seg.ErrBadSeq},
		{"neg seq", api.Event{Sid: "s", Seq: -1, Value: 1}, seg.ErrBadSeq},
		{"big value", api.Event{Sid: "s", Seq: 1, Value: 10}, seg.ErrBadValue},
		{"neg value", api.Event{Sid: "s", Seq: 1, Value: -1}, seg.ErrBadValue},
	}
	for _, c := range cases {
		a := api.New()
		if err := a.Append(c.ev); !errors.Is(err, c.want) {
			t.Fatalf("%s: want %v, got %v", c.name, c.want, err)
		}
		if _, closed, _ := a.Result("s"); closed {
			t.Fatalf("%s: rejected op left trace", c.name)
		}
	}
	a := api.New()
	a.Append(api.Event{Sid: "s", Seq: 1, Value: 4})
	if err := a.Append(api.Event{Sid: "s", Seq: 1, Value: 9}); !errors.Is(err, seg.ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
	if err := a.Close("s", 2); !errors.Is(err, seg.ErrIncomplete) {
		t.Fatalf("want ErrIncomplete, got %v", err)
	}
	if err := a.Close("s", 1); err != nil {
		t.Fatalf("session unusable after rejections: %v", err)
	}
	if r, closed, _ := a.Result("s"); !closed || r != 4 {
		t.Fatalf("result = %d,%v, want 4,true", r, closed)
	}
	kinds := []error{mrg.ErrEmptySid, seg.ErrBadSeq, seg.ErrBadValue, seg.ErrConflict, seg.ErrIncomplete, seg.ErrClosed}
	for i := range kinds {
		for j := i + 1; j < len(kinds); j++ {
			if errors.Is(kinds[i], kinds[j]) {
				t.Fatalf("sentinels not distinct: %v vs %v", kinds[i], kinds[j])
			}
		}
	}
}
