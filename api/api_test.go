package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"slices"
	"sort"
	"sync"
	"testing"

	"ontology/api"
	"ontology/sched"
)

type gent struct {
	id   string
	f    int64
	dead bool
}
type refQ struct{ v []gent }

func (r *refQ) idx(id string) int {
	return slices.IndexFunc(r.v, func(g gent) bool { return g.id == id && !g.dead })
}
func (r *refQ) push(g gent) bool  { r.v = append(r.v, g); return true }
func (r *refQ) killAt(i int) bool { r.v[i].dead = true; return true }
func (r *refQ) sched(id string, f int64) bool {
	return id != "" && r.idx(id) < 0 && r.push(gent{id, f, false})
}
func (r *refQ) cancel(id string) bool { i := r.idx(id); return i >= 0 && r.killAt(i) }
func (r *refQ) due(now int64) []string {
	d := slices.DeleteFunc(slices.Clone(r.v), func(g gent) bool { return g.dead || g.f > now })
	sort.SliceStable(d, func(i, j int) bool { return d[i].f < d[j].f })
	out := make([]string, len(d))
	for i, g := range d {
		out[i] = g.id
		r.cancel(g.id)
	}
	return out
}

type op struct {
	k  int
	id string
	v  int64
}

func drive(t *testing.T, ops []op) {
	q, r := api.New(), &refQ{}
	for _, o := range ops {
		ok := true
		switch o.k {
		case 0:
			ok = (q.Schedule(o.id, o.v) == nil) == r.sched(o.id, o.v)
		case 1:
			ok = (q.Cancel(o.id) == nil) == r.cancel(o.id)
		default:
			g, e := q.Tick(o.v)
			ok = e == nil && reflect.DeepEqual(g, r.due(o.v))
		}
		if !ok {
			t.Fatalf("op %+v mismatch", o)
		}
	}
}

func TestNaiveReference(t *testing.T) {
	for _, seed := range []int64{1, 7, 42} {
		rng, ops, now := rand.New(rand.NewSource(seed)), []op{}, int64(0)
		for range 300 {
			id := fmt.Sprintf("id%d", rng.Intn(10))
			switch rng.Intn(3) {
			case 0:
				ops = append(ops, op{0, id, now + int64(rng.Intn(8)) - 3})
			case 1:
				ops = append(ops, op{1, id, 0})
			default:
				now += int64(rng.Intn(3))
				ops = append(ops, op{2, "", now})
			}
		}
		drive(t, ops)
	}
}

// TestMonotonicClock pins I3: equal now does not re-fire; rewind leaves none.
func TestMonotonicClock(t *testing.T) {
	drive(t, []op{{0, "a", 1}, {2, "", 1}, {2, "", 1}})
	q := api.New()
	_, _ = q.Tick(5)
	if _, e := q.Tick(4); !errors.Is(e, sched.ErrClockRewind) {
		t.Fatal("want ErrClockRewind")
	}
	if g, _ := q.Tick(5); len(g) != 0 {
		t.Fatalf("rewind left a trace %v", g)
	}
}

// TestRejectedOpsNoTrace pins I4, distinct sentinels, and SelfCheck ([d b a c]).
func TestRejectedOpsNoTrace(t *testing.T) {
	drive(t, []op{{0, "", 1}, {0, "a", 5}, {0, "b", 5}, {2, "", 5}})
	drive(t, []op{{0, "a", 5}, {0, "a", 6}, {2, "", 5}})
	drive(t, []op{{1, "ghost", 0}, {0, "y", 2}, {2, "", 2}})
	s := []error{sched.ErrEmptyID, sched.ErrDuplicateID, sched.ErrCancelNotActive, sched.ErrClockRewind}
	for i := range s {
		for j := i + 1; j < len(s); j++ {
			if s[i] == s[j] || !api.New().SelfCheck() {
				t.Fatal("sentinels not distinct or SelfCheck failed")
			}
		}
	}
}

// TestConcurrentCancel: random-order cancels, no sleeps; only survivors fire.
func TestConcurrentCancel(t *testing.T) {
	const n = 256
	q := api.New()
	for i := range n {
		_ = q.Schedule(fmt.Sprintf("k%d", i), 10)
	}
	var wg sync.WaitGroup
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				q.Peek()
				q.SelfCheck()
			}
		}()
	}
	wg.Add(1)
	go func() { defer wg.Done(); _, _ = q.Tick(9) }()
	for _, v := range rand.Perm(n / 2) {
		wg.Add(1)
		go func(v int) { defer wg.Done(); _ = q.Cancel(fmt.Sprintf("k%d", 2*v)) }(v)
	}
	wg.Wait()
	got, err := q.Tick(10)
	if err != nil || len(got) != n/2 {
		t.Fatalf("fired %d err %v", len(got), err)
	}
	for i := 1; i < n; i += 2 {
		if !slices.Contains(got, fmt.Sprintf("k%d", i)) {
			t.Fatalf("survivor k%d missing", i)
		}
	}
}
