package ontology

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

// 两个 goroutine 同时给同一 ONE_TO_ONE 源建链，必须恰好一个成功。
func TestConcurrentOneToOneSource(t *testing.T) {
	s := newStore(t)
	declare(t, s, ltSpouse())
	addObjects(t, s, "User", "u0")
	const n = 32
	for i := 0; i < n; i++ {
		must(t, s.AddObject("User", string(rune('a'+i))+"-t"))
	}
	var okCount atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tgt := key("User", string(rune('a'+i))+"-t")
			if err := s.Link("spouse", key("User", "u0"), tgt); err == nil {
				okCount.Add(1)
			} else {
				var ce *CardinalityError
				if !errors.As(err, &ce) {
					t.Errorf("loser must get CardinalityError, got %v", err)
				}
			}
		}(i)
	}
	wg.Wait()
	if got := okCount.Load(); got != 1 {
		t.Fatalf("exactly one link must succeed, got %d", got)
	}
	if got := len(s.LinksFrom("spouse", key("User", "u0"))); got != 1 {
		t.Fatalf("source must have exactly one link, got %d", got)
	}
	requireInvariantOK(t, s)
}

// 并发抢占同一 ONE_TO_ONE 目标，同样必须恰好一个成功。
func TestConcurrentOneToOneTarget(t *testing.T) {
	s := newStore(t)
	declare(t, s, ltSpouse())
	addObjects(t, s, "User", "v0")
	const n = 32
	for i := 0; i < n; i++ {
		must(t, s.AddObject("User", string(rune('a'+i))+"-s"))
	}
	var okCount atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := s.Link("spouse", key("User", string(rune('a'+i))+"-s"), key("User", "v0")); err == nil {
				okCount.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if got := okCount.Load(); got != 1 {
		t.Fatalf("exactly one link must succeed, got %d", got)
	}
	requireInvariantOK(t, s)
}

// 并发建链/断链/删除期间，任何时刻外部观察到的正反索引都必须互为镜像。
func TestConcurrentMirrorInvariant(t *testing.T) {
	s := newStore(t)
	declare(t, s, ltOwns())
	declare(t, s, ltMember())
	addObjects(t, s, "Team", "t1", "t2")
	const users, docs = 16, 16
	for i := 0; i < users; i++ {
		must(t, s.AddObject("User", string(rune('a'+i))))
	}
	for i := 0; i < docs; i++ {
		must(t, s.AddObject("Doc", string(rune('A'+i))))
	}
	stop := make(chan struct{})
	var observers, writers sync.WaitGroup
	// 观察者：持续自检不变量。
	for w := 0; w < 2; w++ {
		observers.Add(1)
		go func() {
			defer observers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if vs := s.CheckInvariant(); len(vs) > 0 {
					t.Errorf("invariant violated during concurrency: %v", vs[0])
					return
				}
			}
		}()
	}
	// 写者：建链、断链、级联删除混合施压。
	for w := 0; w < 4; w++ {
		writers.Add(1)
		go func(w int) {
			defer writers.Done()
			for i := 0; i < 200; i++ {
				u := key("User", string(rune('a'+(i+w)%users)))
				d := key("Doc", string(rune('A'+(i*7+w)%docs)))
				tm := key("Team", []string{"t1", "t2"}[(i+w)%2])
				_ = s.Link("owns", u, d)
				_ = s.Link("member", tm, u)
				_ = s.Unlink("owns", u, d)
				if i%50 == 49 {
					_ = s.DeleteObject(tm)
				}
			}
		}(w)
	}
	writers.Wait()
	close(stop)
	observers.Wait()
	requireInvariantOK(t, s)
}
