package api

import (
	"cmp"
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"sync"
	"testing"
)

func mk(k string, s int64, a bool) Op { return Op{Row: Row{Key: k, Score: s}, Add: a} }
func chk(t *testing.T, c bool, f string, a ...any) {
	if !c {
		t.Fatalf(f, a...)
	}
}
func tenOps() []Op {
	return []Op{
		mk("a", 50, true), mk("b", 70, true), mk("c", 50, true), mk("d", 60, true), mk("e", 50, true),
		mk("b", 70, false), mk("e", 50, false), mk("f", 55, true), mk("d", 60, false), mk("g", 45, true)}
}
func bruteTop(m map[string]int64, n int) []Row {
	r := make([]Row, 0, len(m))
	for k, s := range m {
		r = append(r, Row{Key: k, Score: s})
	}
	slices.SortFunc(r, func(a, b Row) int { return cmp.Or(cmp.Compare(b.Score, a.Score), cmp.Compare(a.Key, b.Key)) })
	return r[:min(n, len(r))]
}
func mustV(n, m int) *View {
	v, err := New(n, m)
	if err != nil {
		panic(err)
	}
	return v
}
func TestTenSteps(t *testing.T) {
	v := mustV(3, 16)
	want := [][]Change{
		{mk("a", 50, true)}, {mk("b", 70, true)}, {mk("c", 50, true)},
		{mk("c", 50, false), mk("d", 60, true)}, nil, {mk("b", 70, false), mk("c", 50, true)},
		nil, {mk("c", 50, false), mk("f", 55, true)}, {mk("d", 60, false), mk("c", 50, true)}, nil}
	for i, o := range tenOps() {
		got, err := v.Apply([]Op{o})
		chk(t, err == nil && fmt.Sprint(got) == fmt.Sprint(want[i]), "step %d = %v,%v", i+1, got, err)
	}
	fin := []Row{{Key: "f", Score: 55}, {Key: "a", Score: 50}, {Key: "c", Score: 50}}
	chk(t, slices.Equal(v.View(), fin), "final %v", v.View())
}
func TestBatchConsistencyRandom(t *testing.T) {
	for _, seed := range []int64{1, 2, 7, 42} {
		r, v, m := rand.New(rand.NewSource(seed)), mustV(4, 64), map[string]int64{}
		for t0 := 0; t0 < 400; t0++ {
			k := fmt.Sprintf("k%d", r.Intn(24))
			s, live := m[k]
			if !live && r.Intn(2) == 0 {
				s = int64(r.Intn(10))
				if _, e := v.Apply([]Op{mk(k, s, true)}); e == nil {
					m[k] = s
				}
			} else if live {
				if _, e := v.Apply([]Op{mk(k, s, false)}); e == nil {
					delete(m, k)
				}
			}
			chk(t, slices.Equal(v.View(), bruteTop(m, 4)), "seed %d t%d", seed, t0)
		}
	}
}
func TestChangelogPrefix(t *testing.T) {
	v, h := mustV(3, 16), map[string]Row{}
	for i, o := range tenOps() {
		ch, _ := v.Apply([]Op{o})
		for _, c := range ch {
			cur, ok := h[c.Row.Key]
			chk(t, c.Add != ok && (c.Add || cur == c.Row), "step %d prefix", i+1)
			if c.Add {
				h[c.Row.Key] = c.Row
			} else {
				delete(h, c.Row.Key)
			}
		}
		chk(t, len(h) == min(3, v.Live()), "step %d holds %d", i+1, len(h))
	}
}
func TestSentinelErrors(t *testing.T) {
	chk(t, ErrKeyExists != ErrRowMissing && ErrRowMissing != ErrOverLimit &&
		ErrOverLimit != ErrInvalidParam && ErrKeyExists != ErrOverLimit, "not distinct")
	type tc struct {
		n, mx int
		ops   []Op
		want  error
	}
	for _, c := range []tc{
		{0, 4, nil, ErrInvalidParam}, {3, 2, nil, ErrInvalidParam},
		{2, 8, []Op{mk("a", 1, true), mk("a", 2, true)}, ErrKeyExists},
		{2, 8, []Op{mk("z", 1, false)}, ErrRowMissing},
		{2, 8, []Op{mk("a", 1, true), mk("a", 2, false)}, ErrRowMissing},
		{1, 1, []Op{mk("a", 1, true), mk("b", 2, true)}, ErrOverLimit}} {
		v, err := New(c.n, c.mx)
		if err == nil {
			_, err = v.Apply(c.ops)
		}
		chk(t, errors.Is(err, c.want), "n=%d err=%v", c.n, err)
	}
}
func TestRejectNoTrace(t *testing.T) {
	v := mustV(2, 4)
	_, _ = v.Apply([]Op{mk("a", 1, true), mk("b", 2, true), mk("c", 3, true), mk("d", 4, true)})
	live, view := v.Live(), fmt.Sprint(v.View())
	for _, b := range []Op{mk("a", 9, true), mk("a", 9, false), mk("z", 1, false), mk("e", 5, true)} {
		_, e := v.Apply([]Op{b})
		chk(t, e != nil && v.Live() == live && fmt.Sprint(v.View()) == view, "trace %+v", b)
	}
	_, e := v.Apply([]Op{mk("a", 1, false), mk("e", 5, true), mk("b", 9, false)})
	chk(t, errors.Is(e, ErrRowMissing) && v.Live() == live && fmt.Sprint(v.View()) == view, "atomic live=%d", v.Live())
	_, e = v.Apply([]Op{mk("a", 1, false), mk("e", 5, true)})
	chk(t, e == nil, "reuse %v", e)
}
func TestSelfCheck(t *testing.T) { chk(t, mustV(3, 16).SelfCheck() == nil, "selfcheck") }
func TestConcurrentReaders(t *testing.T) {
	v := mustV(8, 256)
	var ops []Op
	for i := 0; i < 100; i++ {
		ops = append(ops, mk(fmt.Sprintf("k%03d", i), int64((i*7)%53), true))
	}
	_, err := v.Apply(ops)
	chk(t, err == nil, "feed %v", err)
	const g = 16
	var wg sync.WaitGroup
	views := make([][]Row, g)
	start := make(chan struct{})
	for i := 0; i < g; i++ {
		wg.Add(1)
		go func(id int) { defer wg.Done(); <-start; views[id] = v.View() }(i)
	}
	close(start)
	wg.Wait()
	for i := 1; i < g; i++ {
		chk(t, fmt.Sprint(views[i]) == fmt.Sprint(views[0]), "reader %d", i)
	}
}
