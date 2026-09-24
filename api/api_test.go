package api_test

import (
	"errors"
	"reflect"
	"slices"
	"sync"
	"testing"

	"ontology/api"
	"ontology/sched"
)

func TestEightOpScript(t *testing.T) {
	p := api.New()
	p.Submit(1, 1)
	p.Submit(2, 1)
	p.Process()
	p.Submit(3, 3)
	if st := p.Snapshot(); len(st.Suspend) != 1 || st.Suspend[0] != (sched.Frame{Prio: 1, Pos: 1}) {
		t.Fatalf("preemption frame = %v, want [{1 1}]", st.Suspend)
	}
	p.Submit(4, 5)
	p.Process()
	if st := p.Snapshot(); st.Current == nil || *st.Current != (sched.Frame{Prio: 3, Pos: 0}) {
		t.Fatalf("LIFO resume current = %v, want {3 0}", st.Current)
	}
	p.Process()
	p.Process()
	if got := p.Done(); !slices.Equal(got, []int{1, 4, 3, 2}) {
		t.Fatalf("done=%v, want [1 4 3 2]", got)
	}
}

func TestFaults(t *testing.T) {
	if api.ErrBadPrio == api.ErrDupID || api.ErrDupID == api.ErrIdle || api.ErrBadPrio == api.ErrIdle {
		t.Fatal("sentinel errors must be distinct")
	}
	p := api.New()
	if err := p.Submit(1, 0); err != nil {
		t.Fatal(err)
	}
	before := p.Snapshot()
	cases := []struct {
		name string
		op   func() error
		want error
	}{
		{"negative prio", func() error { return p.Submit(2, -1) }, api.ErrBadPrio},
		{"duplicate id", func() error { return p.Submit(1, 0) }, api.ErrDupID},
	}
	for _, c := range cases {
		if err := c.op(); !errors.Is(err, c.want) {
			t.Fatalf("%s: got %v, want %v", c.name, err, c.want)
		}
		if !reflect.DeepEqual(before, p.Snapshot()) {
			t.Fatalf("%s: state changed after rejection", c.name)
		}
	}
	if _, _, err := p.Process(); err != nil { // drain the seeded event
		t.Fatal(err)
	}
	idle := p.Snapshot()
	if _, _, err := p.Process(); !errors.Is(err, api.ErrIdle) {
		t.Fatalf("idle process: got %v, want ErrIdle", err)
	}
	if !reflect.DeepEqual(idle, p.Snapshot()) {
		t.Fatal("idle process changed state")
	}
	if err := p.Submit(2, 1); err != nil { // still usable after rejections
		t.Fatal(err)
	}
	if _, _, err := p.Process(); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentDoneReaders(t *testing.T) {
	p := api.New()
	const M = 400
	for i := 0; i < M; i++ {
		p.Submit(i, i%7)
		if i < M/2 { // half processed up front, half during concurrent reads
			p.Process()
		}
	}
	start := make(chan struct{})
	fin := make(chan struct{})
	go func() { // the single goroutine advancing Process
		<-start
		for i := M / 2; i < M; i++ {
			p.Process()
		}
		close(fin)
	}()
	var wg sync.WaitGroup
	errs := make(chan string, 8)
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			var prev []int
			for {
				select {
				case <-fin:
					return
				default:
				}
				cur := p.Done()
				if len(cur) < len(prev) || !slices.Equal(cur[:len(prev)], prev) {
					errs <- "Done() is not a growing prefix of the final order"
					return
				}
				prev = cur
				if err := api.SelfCheck(); err != nil {
					errs <- err.Error()
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	final := p.Done()
	if len(final) != M {
		t.Fatalf("done len=%d, want %d", len(final), M)
	}
	results := make([][]int, 8)
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = p.Done()
		}(r)
	}
	wg.Wait()
	for _, got := range results { // all readers observe the identical order
		if !slices.Equal(got, final) {
			t.Fatal("concurrent readers disagree on Done()")
		}
	}
}
