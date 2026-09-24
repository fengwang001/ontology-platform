package reb

import (
	"errors"
	"math/rand"
	"reflect"
	"testing"

	"ontology/agg"
)

func must(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}
func chk(t *testing.T, ok bool, msg string, args ...any) {
	t.Helper()
	if !ok {
		t.Fatalf(msg, args...)
	}
}
func genSrc(n, seed int) []string {
	r := rand.New(rand.NewSource(int64(seed)))
	v := []string{"a", "b", "c", "d", "e"}
	s := make([]string, n)
	for i := range s {
		s[i] = v[r.Intn(len(v))]
	}
	return s
}
func buildWithCrashes(t *testing.T, m *Machine, seed int64) {
	r := rand.New(rand.NewSource(seed))
	must(t, m.Start())
	for m.processed < len(m.src) {
		for k := r.Intn(4); k > 0 && m.processed < len(m.src); k-- {
			must(t, m.Step())
		}
		if m.processed < len(m.src) && r.Intn(2) == 0 {
			must(t, m.Crash())
			must(t, m.Start())
		}
	}
	if !m.building {
		must(t, m.Start())
	}
	_, err := m.Commit()
	must(t, err)
}
func TestNaiveReplayConsistency(t *testing.T) {
	for _, c := range []struct{ n, chunk int }{{0, 1}, {1, 1}, {5, 2}, {17, 3}, {100, 7}, {1000, 10}} {
		for seed := 0; seed < 8; seed++ {
			src := genSrc(c.n, seed)
			m, err := New(src, c.chunk)
			must(t, err)
			buildWithCrashes(t, m, int64(seed*31+1))
			chk(t, reflect.DeepEqual(m.View(), agg.Naive(src)), "n=%d seed=%d view=%v", c.n, seed, m.View())
		}
	}
}
func TestOldViewReadable(t *testing.T) {
	for _, c := range []struct{ n, chunk int }{{6, 2}, {30, 4}, {100, 10}} {
		m, _ := New(genSrc(c.n, 1), c.chunk)
		buildWithCrashes(t, m, 7)
		old, og := m.View(), m.Gen()
		must(t, m.Start())
		for m.processed < len(m.src) {
			must(t, m.Step())
			chk(t, reflect.DeepEqual(m.View(), old) && m.Gen() == og, "leak at p=%d", m.processed)
		}
		must(t, m.Crash())
		chk(t, reflect.DeepEqual(m.View(), old) && m.Gen() == og, "view changed after crash")
	}
}
func TestCrashResume(t *testing.T) {
	m, _ := New([]string{"a", "b", "a", "c", "b", "a"}, 2)
	half, full := map[string]int{"a": 2, "b": 1, "c": 1}, map[string]int{"a": 3, "b": 2, "c": 1}
	must(t, m.Start())
	must(t, m.Step())
	must(t, m.Step())
	chk(t, m.processed == 4 && reflect.DeepEqual(m.shadow, half), "want p=4 shadow=a2b1c1")
	must(t, m.Crash())
	chk(t, !m.building && m.shadow == nil && reflect.DeepEqual(m.cp.shadow, half) && reflect.DeepEqual(m.View(), map[string]int{}), "crash must keep checkpoint and old view only")
	must(t, m.Start())
	chk(t, m.processed == 4 && reflect.DeepEqual(m.shadow, m.cp.shadow), "resume must restore from checkpoint")
	must(t, m.Step())
	g, err := m.Commit()
	must(t, err)
	chk(t, g == 1 && reflect.DeepEqual(m.View(), full), "g=%d view=%v", g, m.View())
}
func TestAppliedCounterBounded(t *testing.T) {
	for _, n := range []int{1000, 10000} {
		src, chunk := genSrc(n, 99), 10
		m, _ := New(src, chunk)
		must(t, m.Start())
		for i := 0; i < 5; i++ {
			must(t, m.Step())
			must(t, m.Crash())
			must(t, m.Start())
		}
		for m.processed < n {
			must(t, m.Step())
		}
		_, err := m.Commit()
		must(t, err)
		chk(t, m.applied <= int64(n+5*chunk) && m.applied == int64(n) && reflect.DeepEqual(m.View(), agg.Naive(src)), "n=%d applied=%d view=%v", n, m.applied, m.View())
	}
}
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	for _, chk2 := range []int{0, -1} {
		m, err := New([]string{"a"}, chk2)
		chk(t, m == nil && errors.Is(err, ErrBadChunk), "chk=%d err=%v", chk2, err)
	}
	src := []string{"a", "b", "a"}
	m, _ := New(src, 2)
	for _, op := range []func() error{m.Step, m.Crash} {
		err := op()
		chk(t, errors.Is(err, ErrNotBuilding), "idle op: %v", err)
	}
	_, errC := m.Commit()
	chk(t, errors.Is(errC, ErrNotBuilding) && m.gen == 0 && m.processed == 0, "idle commit changed state")
	must(t, m.Start())
	chk(t, errors.Is(m.Start(), ErrBusy), "double Start")
	must(t, m.Step())
	snap := m.Snapshot()
	_, errI := m.Commit()
	chk(t, errors.Is(errI, ErrIncomplete) && m.gen == snap.Gen && m.processed == snap.Processed && m.building, "incomplete commit changed state")
	chk(t, len(map[error]bool{ErrBadChunk: true, ErrBusy: true, ErrNotBuilding: true, ErrIncomplete: true}) == 4, "sentinels must be distinct")
	must(t, m.Step())
	g, err := m.Commit()
	must(t, err)
	chk(t, g == 1 && reflect.DeepEqual(m.View(), agg.Naive(src)), "g=%d view=%v", g, m.View())
}
