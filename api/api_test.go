package api_test

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
	"ontology/sw"
)

func setall(p *[]int64, x int64) {
	for i := range *p {
		(*p)[i] = x
	}
}
func readStr(a *api.Array) string { s, _ := a.Read(); return fmt.Sprint(s) }
func readOnce(a *api.Array, reads, tears *int64) {
	s, _ := a.Read()
	x := s[0]
	for _, y := range s[1:] {
		if y != x {
			atomic.AddInt64(tears, 1)
			break
		}
	}
	atomic.AddInt64(reads, 1)
}
func TestNewRejectsNonPositive(t *testing.T) {
	for _, n := range []int{0, -1, -100} {
		a, err := api.New(n)
		if a != nil || !errors.Is(err, api.ErrInvalidSize) {
			t.Fatalf("New(%d) = (%v, %v), want (nil, ErrInvalidSize)", n, a, err)
		}
	}
}
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	a, _ := api.New(8)
	_ = a.Update(func(p *[]int64) { setall(p, 5) })
	seq0, before := a.Seq(), readStr(a)
	if err := a.Update(nil); !errors.Is(err, sw.ErrNilFunc) || a.Seq() != seq0 || readStr(a) != before {
		t.Fatalf("Update(nil): err=%v and must leave no trace", err)
	}
	var inner error
	_ = a.Update(func(p *[]int64) {
		inner = a.Update(func(q *[]int64) { (*q)[0] = 9 })
		(*p)[0] = 6
	})
	if !errors.Is(inner, sw.ErrReentrant) || errors.Is(inner, sw.ErrNilFunc) {
		t.Fatalf("inner err = %v, want distinct ErrReentrant", inner)
	}
	if a.Seq() != seq0+2 || readStr(a) != "[6 5 5 5 5 5 5 5]" {
		t.Fatalf("after outer: seq=%d arr=%s, rejected inner change must not leak", a.Seq(), readStr(a))
	}
	_ = a.Update(func(p *[]int64) { setall(p, 1) }) // stays usable
}
func TestPanicRecovery(t *testing.T) {
	for _, v := range []any{"boom", 42} {
		a, _ := api.New(4)
		_ = a.Update(func(p *[]int64) { setall(p, 7) })
		seq0, before := a.Seq(), readStr(a)
		err := a.Update(func(p *[]int64) { panic(v) })
		if !errors.Is(err, sw.ErrPanicInUpdate) || a.Seq() != seq0+2 || a.Seq()&1 != 0 {
			t.Fatalf("panic v=%v: err=%v seq=%d, want ErrPanicInUpdate and even %d", v, err, a.Seq(), seq0+2)
		}
		if readStr(a) != before {
			t.Fatalf("panicked update changed the array: %s", readStr(a))
		}
		_ = a.Update(func(p *[]int64) { (*p)[0] = 2 }) // stays usable
		if err := a.SelfCheck(); err != nil {
			t.Fatalf("SelfCheck: %v", err)
		}
	}
}
func TestReadDoesNotBlockWriter(t *testing.T) {
	a, _ := api.New(8)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		_ = a.Update(func(p *[]int64) { close(entered); <-release; setall(p, 9) })
		close(done)
	}()
	<-entered
	res := make(chan []int64, 4)
	for i := 0; i < 4; i++ {
		go func() { s, _ := a.Read(); res <- s }()
	}
	close(release)
	<-done // writer finishes right after release; 4 concurrent readers never blocked it
	for i := 0; i < 4; i++ {
		if s := fmt.Sprint(<-res); s != "[9 9 9 9 9 9 9 9]" {
			t.Fatalf("reader got %s, want the committed terminal", s)
		}
	}
}
func TestConcurrentUpdatesSerialize(t *testing.T) {
	a, _ := api.New(2)
	entered, proceed, d2 := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() { _ = a.Update(func(p *[]int64) { close(entered); <-proceed; (*p)[0] = 1 }) }()
	<-entered
	go func() {
		defer close(d2)
		_ = a.Update(func(p *[]int64) { (*p)[0] = 2 })
	}()
	select {
	case <-d2:
		t.Fatal("second Update did not wait for the first to leave")
	default: // it is blocked on the writer lock, not rejected as re-entrant
	}
	close(proceed)
	<-d2
	if readStr(a) != "[2 0]" {
		t.Fatalf("array = %s, want [2 0] (both updates serialized in order)", readStr(a))
	}
}
func TestConcurrentNoTear(t *testing.T) {
	for _, n := range []int{2, 64, 256} {
		a, _ := api.New(n)
		var stop int32
		var wg sync.WaitGroup
		var reads, tears int64
		var ready sync.WaitGroup
		ready.Add(8)
		for r := 0; r < 8; r++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				readOnce(a, &reads, &tears)
				ready.Done()
				for atomic.LoadInt32(&stop) == 0 {
					readOnce(a, &reads, &tears)
				}
			}()
		}
		ready.Wait()
		for v := int64(1); v <= 1000; v++ {
			vv := v
			if err := a.Update(func(p *[]int64) { setall(p, vv) }); err != nil {
				t.Fatal(err)
			}
		}
		atomic.StoreInt32(&stop, 1)
		wg.Wait()
		if reads == 0 || tears != 0 {
			t.Fatalf("n=%d: reads=%d tears=%d, want reads>0 tears=0", n, reads, tears)
		}
	}
}
