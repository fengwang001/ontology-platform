package merge

import (
	"errors"
	"fmt"
	"math/bits"
	"testing"

	"ontology/msrc"
)

func ev(seq, ts int64, key, val string) msrc.Event {
	return msrc.Event{Seq: seq, TS: ts, Key: key, Val: val}
}

// TestNineEventScenario pins the step-by-step table derived in NOTES.md.
func TestNineEventScenario(t *testing.T) {
	m := New()
	for n, evs := range map[string][]msrc.Event{
		"A": {ev(0, 5, "k1", "a1"), ev(1, 7, "k2", "a2"), ev(2, 9, "k1", "a3")},
		"B": {ev(0, 5, "k1", "b1"), ev(1, 8, "k3", "b2"), ev(2, 9, "k1", "b3")},
		"C": {ev(0, 6, "k4", "c1"), ev(1, 7, "k2", "c2"), ev(2, 10, "k5", "c3")},
	} {
		if err := m.AddSource(n, evs); err != nil {
			t.Fatal(err)
		}
	}
	m.Run()
	want := []msrc.Event{
		{Src: "A", Seq: 0, TS: 5, Key: "k1", Val: "a1"},
		{Src: "C", Seq: 0, TS: 6, Key: "k4", Val: "c1"},
		{Src: "A", Seq: 1, TS: 7, Key: "k2", Val: "a2"},
		{Src: "B", Seq: 1, TS: 8, Key: "k3", Val: "b2"},
		{Src: "A", Seq: 2, TS: 9, Key: "k1", Val: "a3"},
		{Src: "C", Seq: 2, TS: 10, Key: "k5", Val: "c3"},
	}
	got := m.Log()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("log:\n got %v\nwant %v", got, want)
	}
	if m.Dups() != 3 {
		t.Fatalf("dups = %d, want 3", m.Dups())
	}
}

// TestFaultInjectionDistinctErrors: the four rejections are mutually
// distinguishable sentinel errors.
func TestFaultInjectionDistinctErrors(t *testing.T) {
	cases := []struct {
		name string
		src  string
		evs  []msrc.Event
		want error
	}{
		{"seq not increasing", "S1", []msrc.Event{ev(1, 1, "a", ""), ev(1, 2, "b", "")}, msrc.ErrSeqNotStrictlyIncreasing},
		{"seq decreasing", "S1", []msrc.Event{ev(2, 1, "a", ""), ev(1, 2, "b", "")}, msrc.ErrSeqNotStrictlyIncreasing},
		{"ts decreasing", "S1", []msrc.Event{ev(0, 5, "a", ""), ev(1, 4, "b", "")}, msrc.ErrTSDecreasing},
		{"empty key", "S1", []msrc.Event{ev(0, 1, "", "")}, msrc.ErrEmptyKey},
		{"dup name", "S0", []msrc.Event{ev(0, 1, "a", "")}, ErrDuplicateName},
	}
	var all []error
	for _, c := range cases {
		m := New()
		if err := m.AddSource("S0", []msrc.Event{ev(0, 1, "z", "")}); err != nil {
			t.Fatal(err)
		}
		err := m.AddSource(c.src, c.evs)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
		seen := false
		for _, e := range all {
			seen = seen || errors.Is(e, err)
		}
		if !seen {
			all = append(all, err)
		}
	}
	for i := range all { // sentinel errors must be mutually distinguishable
		for j := i + 1; j < len(all); j++ {
			if errors.Is(all[i], all[j]) {
				t.Errorf("errors %d and %d indistinguishable: %v", i, j, all[i])
			}
		}
	}
}

// TestRejectedOpsLeaveNoTrace: a rejected AddSource changes nothing.
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	bads := [][]msrc.Event{
		{ev(1, 1, "a", ""), ev(0, 2, "b", "")}, // seq not strictly increasing
		{ev(0, 2, "a", ""), ev(1, 1, "b", "")}, // ts decreasing
		{ev(0, 1, "", "")},                     // empty key
		{ev(0, 1, "a", ""), ev(1, 1, "a", "")}, // dup (key, ts)
	}
	for _, bad := range bads {
		m := New()
		good := []msrc.Event{ev(0, 1, "k", "v")}
		if err := m.AddSource("ok", good); err != nil {
			t.Fatal(err)
		}
		if err := m.AddSource("ok", good); !errors.Is(err, ErrDuplicateName) {
			t.Fatalf("dup name: %v", err)
		}
		if err := m.AddSource("bad", bad); err == nil {
			t.Fatalf("accepted %v", bad)
		}
		m.Run()
		if got := m.Log(); len(got) != 1 || got[0].Key != "k" || m.Dups() != 0 {
			t.Fatalf("state changed after rejection: log=%v dups=%d", got, m.Dups())
		}
	}
}

// TestPickMinComparisonsSublinear: with m single-event sources, the number
// of head comparisons per pickMin stays O(log m), not O(m).
func TestPickMinComparisonsSublinear(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		mg := New()
		for i := 0; i < m; i++ {
			if err := mg.AddSource(fmt.Sprint("s", i), []msrc.Event{ev(0, int64(i), fmt.Sprint("k", i), "")}); err != nil {
				t.Fatal(err)
			}
		}
		maxCmps := 0
		for mg.Step() {
			if mg.cmps > maxCmps {
				maxCmps = mg.cmps
			}
		}
		if limit := 4*bits.Len(uint(m)) + 8; maxCmps > limit {
			t.Errorf("m=%d: max pickMin comparisons %d > %d (linear scan?)", m, maxCmps, limit)
		}
		if got := len(mg.Log()); got != m {
			t.Errorf("m=%d: log len %d", m, got)
		}
	}
}
