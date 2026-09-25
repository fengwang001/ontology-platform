package api

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"sort"
	"sync"
	"testing"
)

func naiveDue(live []mrec, now int64) ([]mrec, []mrec) {
	due, kept := []mrec{}, live[:0]
	for _, r := range live {
		if !r.canceled && r.t <= now {
			due = append(due, r)
		} else if !r.canceled {
			kept = append(kept, r)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		return due[i].t < due[j].t || due[i].t == due[j].t && due[i].seq < due[j].seq
	})
	return due, kept
}
func TestNaiveReference(t *testing.T) {
	for _, c := range []struct {
		seed int64
		n    int
	}{{1, 300}, {7, 300}, {42, 600}} {
		q, rng := New(), rand.New(rand.NewSource(c.seed))
		var live []mrec
		var seq, now int64
		active := map[string]bool{}
		bad := func(cond bool, what string) {
			if cond {
				t.Fatalf("seed %d %s judgment wrong", c.seed, what)
			}
		}
		for step := 0; step < c.n; step++ {
			id := fmt.Sprintf("id%d", rng.Intn(6))
			switch rng.Intn(3) {
			case 0:
				fire := rng.Int63n(20) - 5
				err := q.Schedule(id, fire)
				bad((err != nil) != active[id], "schedule")
				if err == nil {
					seq++
					live = append(live, mrec{id, fire, seq, false})
					active[id] = true
				}
			case 1:
				err := q.Cancel(id)
				bad((err != nil) != !active[id], "cancel")
				if err == nil {
					for i := range live {
						if live[i].id == id {
							live[i].canceled = true
						}
					}
					delete(active, id)
				}
			default:
				now += rng.Int63n(3)
				got, err := q.Tick(now)
				if err != nil {
					t.Fatal(err)
				}
				want, kept := naiveDue(live, now)
				if len(got) != len(want) {
					t.Fatalf("seed %d now %d: got %v want %v", c.seed, now, got, want)
				}
				for i, r := range want {
					if got[i] != r.id {
						t.Fatalf("seed %d now %d: got %v want %v", c.seed, now, got, want)
					}
					delete(active, r.id)
				}
				live = kept
			}
		}
	}
}
func TestClockMonotonic(t *testing.T) {
	for _, c := range []struct {
		t1, t2 int64
		want   error
		fires  []string
	}{{5, 4, ErrClockRewind, nil}, {5, 5, nil, nil}, {1, 2, nil, []string{"b"}}} {
		q := New()
		_ = q.Schedule("a", 1)
		_ = q.Schedule("b", 2)
		_, _ = q.Tick(c.t1)
		got, err := q.Tick(c.t2)
		if !errors.Is(err, c.want) || err == nil && !slices.Equal(got, c.fires) {
			t.Fatalf("got %v err %v want %v", got, err, c.want)
		}
	}
}
func TestRejectedNoTrace(t *testing.T) {
	q := New()
	_ = q.Schedule("dup", 9)
	_, _ = q.Tick(5)
	bad := []func() error{
		func() error { return q.Schedule("", 1) }, func() error { return q.Schedule("dup", 2) },
		func() error { return q.Cancel("z") }, func() error { _, e := q.Tick(4); return e },
	}
	for _, f := range bad {
		if f() == nil {
			t.Fatal("expected a rejection")
		}
	}
	if got, _ := q.Tick(6); len(got) != 0 {
		t.Fatalf("rejected op left a trace: %v", got)
	}
	if err := q.Schedule("ok", 7); err != nil {
		t.Fatalf("queue unusable: %v", err)
	}
	if got, _ := q.Tick(7); !slices.Equal(got, []string{"ok"}) {
		t.Fatalf("got %v want [ok]", got)
	}
}
func TestConcurrentCancel(t *testing.T) {
	for _, N := range []int{10, 100, 1000} {
		q := New()
		for i := range N {
			if err := q.Schedule(fmt.Sprintf("t%04d", i), 100); err != nil {
				t.Fatal(err)
			}
		}
		rng := rand.New(rand.NewSource(int64(N)))
		killed := map[string]bool{}
		var wg sync.WaitGroup
		for _, i := range rng.Perm(N)[:N/2] {
			id := fmt.Sprintf("t%04d", i)
			killed[id] = true
			wg.Add(1)
			go func() { defer wg.Done(); _ = q.Cancel(id) }()
		}
		wg.Wait()
		got, err := q.Tick(100)
		if err != nil || len(got) != N-N/2 {
			t.Fatalf("N=%d fired %d (err %v), want %d", N, len(got), err, N-N/2)
		}
		if slices.ContainsFunc(got, func(id string) bool { return killed[id] }) {
			t.Fatalf("N=%d a canceled id fired: %v", N, got)
		}
	}
}
