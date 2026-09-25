package api_test

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
)

// 不变量 4：三类失败可判定、互不相同、不留痕，被拒后仍可正常使用。
func TestFailureAtomicity(t *testing.T) {
	for _, p := range []int{3, 17} {
		if _, err := api.New(p); !errors.Is(err, api.ErrBadPrecision) {
			t.Fatalf("New(%d) err=%v, want ErrBadPrecision", p, err)
		}
	}
	a, _ := api.New(4)
	b, _ := api.New(5)
	a.Add("x")
	before := a.Registers()
	if err := a.Merge(b); !errors.Is(err, api.ErrMismatch) {
		t.Fatalf("merge mismatch err=%v, want ErrMismatch", err)
	}
	if !reflect.DeepEqual(before, a.Registers()) {
		t.Fatal("state changed after rejected merge")
	}
	var z api.Sketch
	if err := z.Add("k"); !errors.Is(err, api.ErrNotInit) {
		t.Fatalf("zero Add err=%v, want ErrNotInit", err)
	}
	if _, err := z.Estimate(); !errors.Is(err, api.ErrNotInit) {
		t.Fatalf("zero Estimate err=%v, want ErrNotInit", err)
	}
	if err := z.Merge(a); !errors.Is(err, api.ErrNotInit) {
		t.Fatalf("zero Merge err=%v, want ErrNotInit", err)
	}
	if api.ErrBadPrecision == api.ErrMismatch || api.ErrMismatch == api.ErrNotInit ||
		api.ErrBadPrecision == api.ErrNotInit {
		t.Fatal("sentinel errors must be distinct")
	}
	// 被拒后仍可正常使用
	if err := a.Add("y"); err != nil {
		t.Fatalf("usable after rejection: %v", err)
	}
	if _, err := a.Estimate(); err != nil {
		t.Fatalf("estimate after rejection: %v", err)
	}
	c, _ := api.New(4)
	c.Add("z")
	if err := a.Merge(c); err != nil {
		t.Fatalf("merge after rejection: %v", err)
	}
}

// 并发：N 个 goroutine 并发 Add 互不重复的 key，最终估计落在 ±3σ 内，
// 期间并发读到的 Estimate 单调不减。
func TestConcurrentAddMonotonic(t *testing.T) {
	for _, p := range []int{10, 12} {
		s, _ := api.New(p)
		const G, per = 8, 500
		var wg sync.WaitGroup
		var stop atomic.Bool
		var mono atomic.Bool
		mono.Store(true)
		go func() {
			prev := 0.0
			for !stop.Load() {
				if e, err := s.Estimate(); err == nil {
					if e < prev {
						mono.Store(false)
					}
					prev = e
				}
			}
		}()
		for g := 0; g < G; g++ {
			wg.Add(1)
			go func(g int) {
				defer wg.Done()
				for i := 0; i < per; i++ {
					s.Add(fmt.Sprintf("cc-%d-%d-%d", p, g, i))
				}
			}(g)
		}
		wg.Wait()
		stop.Store(true)
		est, err := s.Estimate()
		if err != nil {
			t.Fatal(err)
		}
		m := float64(uint(1) << p)
		if rel := math.Abs(est-G*per) / (G * per); rel > 3*1.04/math.Sqrt(m) {
			t.Errorf("p=%d: estimate %v rel err %v > 3σ", p, est, rel)
		}
		if !mono.Load() {
			t.Errorf("p=%d: concurrent estimates not monotone", p)
		}
	}
}

// SelfCheck 通过，且与 Estimate 并发调用安全（-race 钉住）。
func TestSelfCheck(t *testing.T) {
	s, _ := api.New(10)
	for i := 0; i < 1000; i++ {
		s.Add(fmt.Sprintf("sc-%d", i))
	}
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := s.SelfCheck(); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := s.Estimate(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}
