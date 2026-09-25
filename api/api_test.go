package api_test

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"ontology/api"
)

func snapStr(p *api.Phaser) string {
	ph, ps, un := p.Snapshot()
	return fmt.Sprint(ph, ps, un)
}
func rec(t *testing.T, p *api.Phaser, got *[]string) func(int, error) {
	t.Helper()
	return func(ph int, err error) {
		if err != nil {
			t.Fatal(err)
		}
		*got = append(*got, fmt.Sprintf("%d|%s", ph, snapStr(p)))
	}
}

func TestFourStepScenario(t *testing.T) {
	p, _ := api.NewPhaser(2)
	var got []string
	r := rec(t, p, &got)
	r(p.Arrive(0))
	c, _, err := p.Register()
	if err != nil || c != 2 {
		t.Fatalf("Register=(%d,%v), want (2,nil)", c, err)
	}
	got = append(got, snapStr(p))
	r(p.Arrive(1))
	r(p.Arrive(2))
	want := "[0|0 [0 1] [1] 0 [0 1 2] [1 2] 0|0 [0 1 2] [2] 0|1 [0 1 2] [0 1 2]]"
	if fmt.Sprint(got) != want {
		t.Fatalf("got %v want %s", got, want)
	}
}
func TestArriveThenDeregister(t *testing.T) {
	p, _ := api.NewPhaser(2)
	var got []string
	r := rec(t, p, &got)
	r(p.Arrive(1))
	r(p.ArriveAndDeregister(0))
	if fmt.Sprint(got) != "[0|0 [0 1] [0] 0|1 [1] [1]]" {
		t.Fatalf("got %v", got)
	}
	if _, err := p.ArriveAndDeregister(1); err != nil || p.Phase() != -1 {
		t.Fatalf("full deregistration: err=%v phase=%d, want nil and -1", err, p.Phase())
	}
}

func TestSentinelErrorsDistinct(t *testing.T) {
	_, e1 := api.NewPhaser(0)
	p, _ := api.NewPhaser(2)
	_, e2 := p.Arrive(9)
	_, _ = p.Arrive(0)
	_, e3 := p.Arrive(0)
	_, _ = p.Arrive(1)
	_, _ = p.ArriveAndDeregister(0)
	_, _ = p.ArriveAndDeregister(1)
	_, e4 := p.Arrive(0)
	_, _, e5 := p.Register()
	_, e6 := p.ArriveAndDeregister(0)
	got := []error{e1, e2, e3, e4, e5, e6}
	want := []error{api.ErrNoParties, api.ErrUnknownID, api.ErrDuplicate,
		api.ErrTerminated, api.ErrTerminated, api.ErrTerminated}
	for i := range got {
		if !errors.Is(got[i], want[i]) {
			t.Fatalf("err %d: got %v want %v", i, got[i], want[i])
		}
	}
	s := []error{api.ErrNoParties, api.ErrUnknownID, api.ErrDuplicate, api.ErrTerminated}
	if s[0] == s[1] || s[0] == s[2] || s[0] == s[3] || s[1] == s[2] || s[1] == s[3] || s[2] == s[3] {
		t.Fatal("sentinels not distinct")
	}
}

func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	ops := map[string]func(p *api.Phaser) error{
		"duplicate arrive":   func(p *api.Phaser) error { _, e := p.Arrive(0); return e },
		"unknown arrive":     func(p *api.Phaser) error { _, e := p.Arrive(99); return e },
		"unknown deregister": func(p *api.Phaser) error { _, e := p.ArriveAndDeregister(99); return e },
	}
	for name, op := range ops {
		p, _ := api.NewPhaser(2)
		_, _ = p.Arrive(0)
		before := snapStr(p)
		if err := op(p); err == nil {
			t.Fatalf("%s: want error", name)
		}
		if got := snapStr(p); got != before {
			t.Fatalf("%s: state changed: %q -> %q", name, before, got)
		}
		if _, err := p.Arrive(1); err != nil || p.Phase() != 1 {
			t.Fatalf("%s: unusable after rejection: err=%v phase=%d", name, err, p.Phase())
		}
	}
}
func TestAwaitAdvance(t *testing.T) {
	p, _ := api.NewPhaser(2)
	_, _ = p.Arrive(0)
	done := make(chan int, 1)
	go func() { done <- p.AwaitAdvance(0) }()
	_, _ = p.Arrive(1)
	if got := <-done; got != 1 {
		t.Fatalf("AwaitAdvance(0)=%d, want 1", got)
	}
	if got := p.AwaitAdvance(0); got != 1 {
		t.Fatalf("already advanced: AwaitAdvance(0)=%d, want 1", got)
	}
}

func TestConcurrentRounds(t *testing.T) {
	const N, K = 16, 100
	p, _ := api.NewPhaser(N)
	stops := make([][]int, N)
	var finished atomic.Int64
	done := make(chan struct{})
	for i := 0; i < N; i++ {
		go func(id int) {
			stops[id] = make([]int, K)
			for r := 0; r < K; r++ {
				ph, _ := p.Arrive(id)
				stops[id][r] = p.AwaitAdvance(ph)
			}
			if finished.Add(1) == N {
				close(done)
			}
		}(i)
	}
	<-done
	for i := 1; i < N; i++ {
		if fmt.Sprint(stops[i]) != fmt.Sprint(stops[0]) {
			t.Fatalf("party %d stops differ from party 0", i)
		}
	}
	if p.Phase() != K {
		t.Fatalf("Phase()=%d, want %d", p.Phase(), K)
	}
}
func TestSelfCheck(t *testing.T) {
	if err := new(api.Phaser).SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
