package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"ontology/drain"
)

type check struct {
	name string
	ok   bool
}

func report(c check) {
	status := "FAIL"
	if c.ok {
		status = "OK"
	}
	fmt.Printf("[%s] %s\n", status, c.name)
}

func main() {
	failed := false
	fail := func(c *check) { c.ok = false; failed = true }

	// 1. 停机后拒绝新请求
	{
		c := check{name: "1 reject new requests after shutdown", ok: true}
		g := drain.New(time.Now)
		r1, e1 := g.Enter()
		go g.Shutdown(time.Now().Add(time.Hour))
		time.Sleep(20 * time.Millisecond)
		r2, e2 := g.Enter()
		s := g.Stats()
		if e1 != nil || !errors.Is(e2, drain.ErrShuttingDown) || r2 != nil ||
			s.Admitted != 1 || s.Rejected != 1 {
			fail(&c)
		}
		r1()
		time.Sleep(20 * time.Millisecond)
		report(c)
	}

	// 2. 在途归零立即返回，不多等
	{
		c := check{name: "2 wait for in-flight, return instantly at zero", ok: true}
		g := drain.New(time.Now)
		rel, _ := g.Enter()
		done := make(chan error, 1)
		go func() { done <- g.Shutdown(time.Now().Add(time.Hour)) }()
		time.Sleep(20 * time.Millisecond)
		select {
		case <-done:
			fail(&c)
		default:
		}
		start := time.Now()
		rel()
		if err := <-done; err != nil || time.Since(start) > 200*time.Millisecond {
			fail(&c)
		}
		report(c)
	}

	// 3. 超时归因：ErrDrainTimeout + InFlight 保留 + Done=true
	{
		c := check{name: "3 timeout keeps real InFlight and marks Done", ok: true}
		g := drain.New(time.Now)
		r1, _ := g.Enter()
		r2, _ := g.Enter()
		err := g.Shutdown(time.Now().Add(20 * time.Millisecond))
		s := g.Stats()
		if !errors.Is(err, drain.ErrDrainTimeout) || !s.Done || s.InFlight != 2 {
			fail(&c)
		}
		r1()
		r2()
		report(c)
	}

	// 4. release 恰好一次，计数不为负
	{
		c := check{name: "4 release is exactly-once, InFlight never negative", ok: true}
		g := drain.New(time.Now)
		rel, _ := g.Enter()
		var wg sync.WaitGroup
		for range 64 {
			wg.Add(1)
			go func() { defer wg.Done(); rel() }()
		}
		wg.Wait()
		if s := g.Stats(); s.InFlight != 0 {
			fail(&c)
		}
		report(c)
	}

	// 5. Shutdown 幂等：并发调用同一结果、计数不变
	{
		c := check{name: "5 idempotent Shutdown shares one result", ok: true}
		g := drain.New(time.Now)
		rel, _ := g.Enter()
		deadline := time.Now().Add(20 * time.Millisecond)
		var wg sync.WaitGroup
		var timeoutCount int64
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if errors.Is(g.Shutdown(deadline), drain.ErrDrainTimeout) {
					atomic.AddInt64(&timeoutCount, 1)
				}
			}()
		}
		wg.Wait()
		rel()
		s := g.Stats()
		if timeoutCount != 8 || s.Admitted != 1 || s.Rejected != 0 {
			fail(&c)
		}
		report(c)
	}

	// 6. 停机中归还：递减并在归零时唤醒
	{
		c := check{name: "6 releases during drain wake Shutdown at zero", ok: true}
		g := drain.New(time.Now)
		rels := make([]func(), 3)
		for i := range rels {
			rels[i], _ = g.Enter()
		}
		done := make(chan error, 1)
		go func() { done <- g.Shutdown(time.Now().Add(time.Hour)) }()
		for _, r := range rels {
			time.Sleep(10 * time.Millisecond)
			r()
		}
		if err := <-done; err != nil || g.Stats().InFlight != 0 {
			fail(&c)
		}
		report(c)
	}

	// 7. 超时后迟到 release 仍记账到 0
	{
		c := check{name: "7 late releases after timeout still account down", ok: true}
		g := drain.New(time.Now)
		r1, _ := g.Enter()
		r2, _ := g.Enter()
		_ = g.Shutdown(time.Now().Add(20 * time.Millisecond))
		r1()
		if s := g.Stats(); s.InFlight != 1 {
			fail(&c)
		}
		r2()
		if s := g.Stats(); s.InFlight != 0 {
			fail(&c)
		}
		report(c)
	}

	// 8. 并发计数守恒
	{
		c := check{name: "8 concurrent conservation: A==ok, A+R==attempts", ok: true}
		g := drain.New(time.Now)
		var admitted, attempts int64
		var wg sync.WaitGroup
		stop := make(chan struct{})
		go func() {
			time.Sleep(20 * time.Millisecond)
			g.Shutdown(time.Now().Add(5 * time.Second))
			close(stop)
		}()
		for range 16 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range 300 {
					atomic.AddInt64(&attempts, 1)
					rel, err := g.Enter()
					if err != nil {
						continue
					}
					atomic.AddInt64(&admitted, 1)
					rel()
				}
			}()
		}
		wg.Wait()
		<-stop
		s := g.Stats()
		if int64(s.Admitted) != admitted || int64(s.Admitted+s.Rejected) != attempts || s.InFlight != 0 {
			fail(&c)
		}
		report(c)
	}

	if failed {
		os.Exit(1)
	}
}
