package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"

	"ontology/api"
	"ontology/ord"
)

func enterables(g *api.Group, n int) []int {
	got := []int{}
	for pid := 0; pid < n; pid++ {
		if ok, _ := g.Enterable(pid); ok {
			got = append(got, pid)
		}
	}
	return got
}

func TestSelfCheck(t *testing.T) {
	if err := api.New(1).SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

func TestAPIRejectedErrors_TableDriven(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
		want error
	}{
		{"pid negative", api.New(3).Request(-1, 0), api.ErrPIDOutOfRange},
		{"pid too large", api.New(3).Request(3, 0), api.ErrPIDOutOfRange},
		{"negative ts", api.New(3).Request(0, -2), api.ErrNegativeTS},
		{"exit without request", api.New(3).Exit(2), api.ErrNotInterested},
	} {
		t.Run(c.name, func(t *testing.T) {
			if !errors.Is(c.err, c.want) {
				t.Fatalf("got %v, want %v", c.err, c.want)
			}
		})
	}
	g := api.New(2)
	if err := g.Request(0, 4); err != nil {
		t.Fatal(err)
	}
	if err := g.Request(0, 5); !errors.Is(err, api.ErrDuplicateRequest) {
		t.Fatalf("duplicate request: got %v, want %v", err, api.ErrDuplicateRequest)
	}
}

// TestConcurrent_OrderAndSingleEnterable: N goroutines concurrently
// request distinct pids; after Resolve the entry order must equal the
// (ts,pid) total order, and concurrent readers never observe more than
// one enterable process. No sleeps: synchronization is via WaitGroups.
func TestConcurrent_OrderAndSingleEnterable(t *testing.T) {
	const n = 200
	g, tss := api.New(n), make([]int, n)
	var wg sync.WaitGroup
	for pid := 0; pid < n; pid++ {
		tss[pid] = rand.Intn(500)
		wg.Add(1)
		go func(pid, ts int) { defer wg.Done(); _ = g.Request(pid, ts) }(pid, tss[pid])
	}
	wg.Wait()
	g.Resolve()

	want := make([]int, n)
	for i := range want {
		want[i] = i
	}
	sort.Slice(want, func(a, b int) bool {
		return ord.Less(ord.Ticket{TS: tss[want[a]], PID: want[a]}, ord.Ticket{TS: tss[want[b]], PID: want[b]})
	})

	got := make([]int, 0, n)
	for step := 0; step < n; step++ {
		var rw sync.WaitGroup
		counts := make(chan int, 8)
		for r := 0; r < 8; r++ { // 8 concurrent readers each snapshot the set
			rw.Add(1)
			go func() { defer rw.Done(); counts <- len(enterables(g, n)) }()
		}
		rw.Wait()
		close(counts)
		for c := range counts { // invariant 1 under concurrency: never >1
			if c > 1 {
				t.Fatalf("step %d: %d enterable processes at once", step, c)
			}
		}
		cur := enterables(g, n)
		if len(cur) != 1 {
			t.Fatalf("step %d: enterable = %v, want one", step, cur)
		}
		got = append(got, cur[0])
		if err := g.Exit(cur[0]); err != nil {
			t.Fatalf("Exit(%d): %v", cur[0], err)
		}
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("concurrent entry order deviates from total order")
	}
}
