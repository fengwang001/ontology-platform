package check

import (
	"errors"
	"math/rand"
	"ontology/drr"
	"sync"
	"testing"
)

func must(t *testing.T, ok bool, msg string) {
	t.Helper()
	if !ok {
		t.Fatal(msg)
	}
}
func TestAll(t *testing.T) {
	for name, run := range map[string]func(*testing.T){"random": testRandom, "fair+checked": testFairChecked, "errors": testErrors, "idle": testIdle, "concurrent": testConcurrent} {
		t.Run(name, run)
	}
}
func testRandom(t *testing.T) {
	r, s, n := rand.New(rand.NewSource(1)), drr.New(), NewNaive()
	for range 2000 {
		id, sz, q := r.Intn(8), 1+r.Intn(drr.MaxBlock), 100+r.Intn(900)
		switch r.Intn(3) {
		case 0:
			s.AddFlow(id, q)
			n.AddFlow(id, q)
		case 1:
			n.EnqueueIf(s.Enqueue(id, sz) == nil, id, sz)
		default:
			i1, z1, o1 := s.Dequeue()
			i2, z2, o2 := n.Dequeue()
			must(t, i1 == i2 && z1 == z2 && o1 == o2, "dequeue mismatch")
		}
	}
}
func testFairChecked(t *testing.T) {
	s := drr.New()
	for i := range 10002 {
		s.AddFlow(i, 2048)
	}
	for range 400 {
		s.Enqueue(0, drr.MaxBlock)
		s.Enqueue(1, drr.MaxBlock)
		s.Dequeue()
		must(t, s.Stats().Flows[0].Sent-s.Stats().Flows[1].Sent <= 10238 && s.Stats().Flows[1].Sent-s.Stats().Flows[0].Sent <= 10238 && s.Stats().Checked <= 3, "fairness+checked")
	}
}
func testErrors(t *testing.T) {
	s := drr.New()
	s.AddFlow(1, 100)
	for i, err := range []error{drr.ErrUnknownFlow, drr.ErrBadSize, drr.ErrBadSize} {
		must(t, errors.Is(s.Enqueue([]int{9, 1, 1}[i], []int{10, 0, drr.MaxBlock + 1}[i]), err), "sentinel")
	}
	for range drr.MaxQueued {
		s.Enqueue(1, 1)
	}
	must(t, errors.Is(s.Enqueue(1, 1), drr.ErrFull) && s.Stats().Flows[1].Enqueued == drr.MaxQueued, "full, zero side effects")
}
func testIdle(t *testing.T) {
	s := drr.New()
	s.AddFlow(1, 500)
	s.AddFlow(2, 500)
	for range 1000 {
		s.Enqueue(2, 500)
		s.Dequeue()
	}
	for range 10 {
		s.Enqueue(1, 500)
	}
	id, size, ok := s.Dequeue()
	must(t, ok && id == 1 && size <= 500+drr.MaxBlock-1 && 1000*500+500 > 500+drr.MaxBlock-1, "burst bounded; buggy keep-deficit exceeds")
}
func testConcurrent(t *testing.T) {
	s, wg := drr.New(), sync.WaitGroup{}
	for id := range 8 {
		s.AddFlow(id, 1000)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 500 {
				s.Enqueue(id, 100)
			}
		}()
	}
	go func() {
		for range 8 * 500 {
			s.Dequeue()
		}
	}()
	wg.Wait()
	for range 8 * 500 {
		s.Dequeue()
	}
	for _, f := range s.Stats().Flows {
		must(t, f.Sent+f.Queued == f.Enqueued, "conservation")
	}
}
