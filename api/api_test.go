package api_test

import "errors"
import "fmt"
import "math/rand/v2"
import "slices"
import "sync"
import "testing"
import "ontology/api"
import "ontology/ev"
import "ontology/fold"

func mkstream(rng *rand.Rand, n int) []ev.Event {
	s := make([]ev.Event, n)
	for i := range s {
		s[i] = ev.Event{Key: "g", Val: int64(rng.IntN(3)), Op: ev.Op(1 + rng.IntN(2))}
	}
	return s
}
func TestSelfCheck(t *testing.T) {
	if err := api.New(0).SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
func TestInvariants(t *testing.T) {
	a, I, R := api.New(0), ev.Insert, ev.Retract
	six := []ev.Event{{Key: "g", Val: 7, Op: I}, {Key: "g", Val: 7, Op: I}, {Key: "g", Val: 7, Op: R}, {Key: "g", Val: 7, Op: R}, {Key: "g", Val: 7, Op: I}, {Key: "g", Val: 3, Op: I}}
	fat := api.State{"g": {0: 250, 1: 250, 2: 250}} // >=200: random streams stay legal all the way
	cases := [][]ev.Event{six, six, {{Key: "g", Val: 7, Op: R}, {Key: "g", Val: 7, Op: I}}}
	inits := []api.State{nil, api.State{"g": {7: 1}}, api.State{"g": {7: 1}}}
	rng := rand.New(rand.NewPCG(99, 7))
	for _, n := range []int{1, 31, 200} {
		cases, inits = append(cases, mkstream(rng, n)), append(inits, fat)
	}
	for i, s := range cases {
		mp, err := a.Compact(s)
		id, _ := a.Compact(mp)
		if err != nil || len(mp) > len(s) || !slices.Equal(id, mp) {
			t.Fatalf("%d grow/idempotent: %v %d>%d", i, err, len(mp), len(s))
		}
		e1, x1 := a.Replay(inits[i], s)
		e2, x2 := a.Replay(inits[i], mp)
		if x1 == nil && (x2 != nil || fmt.Sprint(e1) != fmt.Sprint(e2)) {
			t.Fatalf("%d invariant 1/3 broken: %v %v", i, x2, e2)
		}
	}
}
func TestSentinels(t *testing.T) {
	maxes := []int{1, 0, 0, 0}
	ss := [][]ev.Event{
		{{Key: "g", Val: 1, Op: ev.Insert}, {Key: "g", Val: 2, Op: ev.Insert}},
		{{Key: "", Op: ev.Insert}},
		{{Key: "g", Op: ev.Op(7)}},
		{{Key: "g", Val: 1, Op: ev.Retract}},
	}
	wants := []error{api.ErrStreamTooLong, ev.ErrInvalidEvent, ev.ErrInvalidEvent, api.ErrIllegalRetract}
	for i := range ss {
		if _, err := api.New(maxes[i]).Replay(nil, ss[i]); !errors.Is(err, wants[i]) {
			t.Errorf("got %v want %v", err, wants[i])
		}
	}
}
func TestReusable(t *testing.T) {
	a, init := api.New(2), api.State{"g": {1: 1}}
	for i, s := range [][]ev.Event{
		{{Key: "g", Val: 1, Op: ev.Insert}, {Key: "g", Val: 2, Op: ev.Insert}, {Key: "g", Val: 3, Op: ev.Insert}},
		{{Key: "", Op: ev.Insert}}, {{Key: "g", Val: 9, Op: ev.Retract}},
	} {
		if _, err := a.Replay(init, s); err == nil {
			t.Errorf("case %d: want error", i)
		}
	}
	if end, err := a.Replay(nil, []ev.Event{{Key: "g", Val: 3, Op: ev.Insert}}); err != nil || fmt.Sprint(init) != "map[g:map[1:1]]" || fmt.Sprint(end) != "map[g:map[3:1]]" {
		t.Fatalf("failure left traces: %v %v %v", err, init, end)
	}
}
func TestLinear(t *testing.T) {
	for _, n := range []int{100, 500, 1000, 5000, 10000} {
		if !fold.New().LinearBound(n, 1) {
			t.Errorf("n=%d comparisons exceeded n", n)
		}
	}
}
func TestConcurrent(t *testing.T) {
	a := api.New(0)
	s := mkstream(rand.New(rand.NewPCG(1, 2)), 500)
	want, _ := a.Compact(s)
	res := make([][]ev.Event, 32)
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Add(1)
		go func() { defer wg.Done(); res[i], _ = a.Compact(s) }()
	}
	wg.Wait()
	for i, r := range res {
		if !slices.Equal(r, want) {
			t.Fatalf("goroutine %d differs", i)
		}
	}
}
