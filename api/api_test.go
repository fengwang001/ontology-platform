package api_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/api"
	"ontology/evt"
)

func TestEightStepSequence(t *testing.T) {
	m := api.New(8)
	want := []struct {
		k string
		v int64
	}{
		{"a", 5}, {"a", 5}, {"a", 3}, {"a", 3},
		{"a", 3}, {"a", 5}, {"b", 5}, {"", 0},
	}
	type step struct {
		add bool
		k   string
		v   int64
	}
	seq := []step{
		{true, "a", 5}, {true, "b", 5}, {true, "a", 3}, {true, "a", 3},
		{false, "a", 3}, {false, "a", 3}, {false, "a", 5}, {false, "b", 5},
	}
	for i, st := range seq {
		var err error
		if st.add {
			err = m.Add(st.k, st.v)
		} else {
			err = m.Remove(st.k, st.v)
		}
		if err != nil {
			t.Fatalf("step %d: unexpected error %v", i+1, err)
		}
		gk, gv, ok := m.First()
		if gk != want[i].k || gv != want[i].v || ok != (want[i].k != "") {
			t.Fatalf("step %d: First=(%s,%d,%v) want (%s,%d)", i+1, gk, gv, ok, want[i].k, want[i].v)
		}
	}
}

func TestRejectedOpsLeaveState(t *testing.T) {
	cases := []struct {
		name string
		fill func(m *api.Manager)
		call func(m *api.Manager) error
		want error
	}{
		{"empty key add", func(m *api.Manager) {}, func(m *api.Manager) error { return m.Add("", 1) }, api.ErrEmptyKey},
		{"empty key remove", func(m *api.Manager) {}, func(m *api.Manager) error { return m.Remove("", 1) }, api.ErrEmptyKey},
		{"capacity", func(m *api.Manager) { m.Add("z", 9) }, func(m *api.Manager) error { return m.Add("y", 8) }, api.ErrCapacity},
		{"not found", func(m *api.Manager) {}, func(m *api.Manager) error { return m.Remove("x", 1) }, api.ErrNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := api.New(1)
			tc.fill(m)
			n := m.Count()
			k, v, _ := m.First()
			if err := tc.call(m); !errors.Is(err, tc.want) {
				t.Fatalf("want %v got %v", tc.want, err)
			}
			if m.Count() != n {
				t.Fatalf("Count changed after rejection: %d -> %d", n, m.Count())
			}
			k2, v2, ok := m.First()
			if k2 != k || v2 != v || ok != (n > 0) {
				t.Fatalf("First changed after rejection")
			}
			// Instance remains usable; drain any pre-fill, then add (negative TS).
			_ = m.Remove("z", 9)
			if err := m.Add("p", -7); err != nil {
				t.Fatalf("instance unusable after rejection: %v", err)
			}
		})
	}
}

func TestConcurrentReadersAgree(t *testing.T) {
	m := api.New(1000)
	// Fill with many keys; smallest (TS,Key) is deterministic.
	for i := 0; i < 500; i++ {
		if err := m.Add(string(rune('A'+i%26))+string(rune('a'+i/26)), int64(i-250)); err != nil {
			t.Fatal(err)
		}
	}
	wantK, wantV, wantOK := m.First()
	const N = 64
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, N)
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for r := 0; r < 200; r++ {
				k, v, ok := m.First()
				c := m.Count()
				if k != wantK || v != wantV || ok != wantOK || c != 500 {
					errs <- errors.New("reader observed inconsistent First/Count")
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestSelfCheck(t *testing.T) {
	if !api.New(0).SelfCheck() {
		t.Fatal("SelfCheck failed")
	}
}

// TestEventOrdering pins evt directly so a reversed comparator cannot hide
// behind the oracle, which uses the same comparator.
func TestEventOrdering(t *testing.T) {
	cases := []struct {
		a, b evt.Event
		less bool
	}{
		{evt.Event{Key: "a", TS: 1}, evt.Event{Key: "b", TS: 2}, true},
		{evt.Event{Key: "b", TS: 1}, evt.Event{Key: "a", TS: 2}, true},
		{evt.Event{Key: "a", TS: 5}, evt.Event{Key: "b", TS: 5}, true},
		{evt.Event{Key: "b", TS: 5}, evt.Event{Key: "a", TS: 5}, false},
		{evt.Event{Key: "a", TS: -3}, evt.Event{Key: "a", TS: -2}, true},
		{evt.Event{Key: "a", TS: 1}, evt.Event{Key: "a", TS: 1}, false},
	}
	for _, tc := range cases {
		if got := evt.Less(tc.a, tc.b); got != tc.less {
			t.Fatalf("Less(%v,%v)=%v want %v", tc.a, tc.b, got, tc.less)
		}
	}
}
