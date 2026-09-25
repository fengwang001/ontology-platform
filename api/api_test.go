package api_test

import (
	"errors"
	"fmt"
	"maps"
	"math/rand"
	"slices"
	"sort"
	"sync"
	"testing"

	"ontology/api"
)

// run applies mini-ops ("p a 10"=Push, "u b 20"=UpdatePriority, "d c"=Delete).
func run(t *testing.T, ops ...string) *api.Queue {
	t.Helper()
	q := api.New()
	fn := map[string]func(string, int) error{"p": q.Push, "u": q.UpdatePriority,
		"d": func(id string, _ int) error { return q.Delete(id) }}
	for _, s := range ops {
		var k, id string
		var p int
		_, _ = fmt.Sscanf(s, "%s %s %d", &k, &id, &p)
		if err := fn[k](id, p); err != nil {
			t.Fatalf("op %q: %v", s, err)
		}
	}
	return q
}

func drain(q *api.Queue) (out []string) {
	for {
		id, _, ok := q.Pop()
		if !ok {
			return
		}
		out = append(out, id)
	}
}

// TestDeleteRepush pins the NOTES.md seven-step derivation; a re-pushed id gets a fresh registration sequence.
func TestDeleteRepush(t *testing.T) {
	q := run(t, "p a 10", "p b 5", "p c 5", "p d 10", "u b 20", "d c", "p c 8")
	if got := drain(q); !slices.Equal(got, []string{"c", "a", "d", "b"}) {
		t.Fatalf("seven-step: got %v want [c a d b]", got)
	}
	q = run(t, "p a 5", "p b 5", "p c 5", "d b", "p b 5")
	if got := drain(q); !slices.Equal(got, []string{"a", "c", "b"}) {
		t.Fatalf("re-push seq: got %v want [a c b]", got)
	}
}

// TestErrors: four distinct decidable errors; rejection leaves no trace.
func TestErrors(t *testing.T) {
	all := []error{api.ErrEmptyID, api.ErrDuplicateID, api.ErrUpdateNotFound, api.ErrDeleteNotFound}
	seen := map[error]bool{}
	for _, e := range all {
		if seen[e] {
			t.Fatal("sentinels not distinct")
		}
		seen[e] = true
	}
	cases := []struct {
		name string
		op   func(q *api.Queue) error
		want error
	}{
		{"empty id", func(q *api.Queue) error { return q.Push("", 1) }, api.ErrEmptyID},
		{"duplicate id", func(q *api.Queue) error { return q.Push("x", 1) }, api.ErrDuplicateID},
		{"update missing", func(q *api.Queue) error { return q.UpdatePriority("g", 1) }, api.ErrUpdateNotFound},
		{"delete missing", func(q *api.Queue) error { return q.Delete("g") }, api.ErrDeleteNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := run(t, "p x 7")
			if err := c.op(q); !errors.Is(err, c.want) {
				t.Fatalf("got %v want %v", err, c.want)
			}
			if q.Len() != 1 || !slices.Equal(drain(q), []string{"x"}) {
				t.Fatal("rejected op left a trace")
			}
		})
	}
}

// TestNaiveReference: random ops must pop like a sorted naive snapshot.
func TestNaiveReference(t *testing.T) {
	type el struct{ pri, seq int }
	for _, seed := range []int64{7, 8, 9} {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			q, model, seq := api.New(), map[string]el{}, 0
			for i := 0; i < 1500; i++ {
				id := fmt.Sprintf("id%d", rng.Intn(100))
				_, live := model[id]
				switch np := rng.Intn(40); rng.Intn(4) {
				case 0, 1:
					if !live && q.Push(id, np) == nil {
						seq++
						model[id] = el{np, seq}
					}
				case 2:
					if live && q.UpdatePriority(id, np) == nil {
						model[id] = el{np, model[id].seq}
					}
				case 3:
					if live && q.Delete(id) == nil {
						delete(model, id)
					}
				}
			}
			ids := slices.Collect(maps.Keys(model))
			sort.Slice(ids, func(i, j int) bool {
				a, b := model[ids[i]], model[ids[j]]
				return a.pri < b.pri || (a.pri == b.pri && a.seq < b.seq)
			})
			if got := drain(q); !slices.Equal(got, ids) {
				t.Fatal("pop order diverges from naive reference")
			}
		})
	}
}

// TestConcurrentDelete: N goroutines delete concurrently; survivors pop once.
func TestConcurrentDelete(t *testing.T) {
	for _, n := range []int{8, 64, 256} {
		t.Run(fmt.Sprintf("n%d", n), func(t *testing.T) {
			q := api.New()
			for i := 0; i < 2*n; i++ {
				_ = q.Push(fmt.Sprintf("id%d", i), i)
			}
			var wg sync.WaitGroup
			for i := 0; i < n; i++ {
				wg.Add(2)
				go func(i int) { defer wg.Done(); _ = q.Delete(fmt.Sprintf("id%d", 2*i)) }(i)
				go func() { defer wg.Done(); _ = q.SelfCheck() }()
			}
			wg.Wait()
			want := make([]string, 0, n)
			for i := 0; i < n; i++ {
				want = append(want, fmt.Sprintf("id%d", 2*i+1))
			}
			if got := drain(q); !slices.Equal(got, want) { // exact survivors, ordered
				t.Fatalf("survivors: got %v", got)
			}
		})
	}
}
