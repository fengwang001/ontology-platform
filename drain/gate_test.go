package drain

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// 语义 1：停机开始后 Enter 一律拒绝，release 为 nil，只增 Rejected。
func TestRejectAfterShutdown(t *testing.T) {
	now := time.Now
	g := New(now)

	r1, err := g.Enter()
	if err != nil || r1 == nil {
		t.Fatalf("first Enter: err=%v, release nil=%v, want nil err and non-nil release", err, r1 == nil)
	}
	r2, err := g.Enter()
	if err != nil || r2 == nil {
		t.Fatalf("second Enter: err=%v, release nil=%v", err, r2 == nil)
	}

	done := make(chan error, 1)
	go func() { done <- g.Shutdown(now().Add(time.Hour)) }()

	// 等待停机真正开始，再尝试进入。
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		g.mu.Lock()
		sd := g.shuttingDown
		g.mu.Unlock()
		if sd {
			break
		}
		time.Sleep(time.Millisecond)
	}

	rel, err := g.Enter()
	if !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("Enter after shutdown err = %v, want ErrShuttingDown", err)
	}
	if rel != nil {
		t.Fatalf("Enter after shutdown returned non-nil release, want nil")
	}

	s := g.Stats()
	if s.Admitted != 2 || s.Rejected != 1 || s.InFlight != 2 {
		t.Fatalf("stats = %+v, want Admitted=2 Rejected=1 InFlight=2", s)
	}

	r1()
	r2()
	if err := <-done; err != nil {
		t.Fatalf("Shutdown = %v, want nil", err)
	}
}

// 语义 2：在途归零立刻返回；调用时已无在途则立即返回，不多等。
func TestShutdownWaitsForInflight(t *testing.T) {
	now := time.Now
	g := New(now)

	rel, err := g.Enter()
	if err != nil {
		t.Fatal(err)
	}

	finished := make(chan error, 1)
	go func() { finished <- g.Shutdown(now().Add(time.Hour)) }()

	time.Sleep(20 * time.Millisecond)
	select {
	case <-finished:
		t.Fatal("Shutdown returned while request still in flight")
	default:
	}

	rel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("Shutdown = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not return when in-flight hit zero")
	}

	g2 := New(now)
	errc := make(chan error, 1)
	go func() { errc <- g2.Shutdown(now().Add(time.Hour)) }()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("idle Shutdown = %v, want nil", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("idle Shutdown should return immediately")
	}
}

// 语义 3 & 7：超时返回 ErrDrainTimeout，InFlight 保留真实值且 Done=true；
// 超时后的迟到 release 仍把 InFlight 记到 0。
func TestTimeoutAttributionAndLateRelease(t *testing.T) {
	g := New(time.Now)

	r1, _ := g.Enter()
	r2, _ := g.Enter()

	start := time.Now()
	err := g.Shutdown(start.Add(30 * time.Millisecond))
	if !errors.Is(err, ErrDrainTimeout) {
		t.Fatalf("Shutdown = %v, want ErrDrainTimeout", err)
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("Shutdown took %v, should return at deadline", d)
	}

	s := g.Stats()
	if !s.Done {
		t.Fatal("Done = false, want true after timeout")
	}
	if s.InFlight != 2 {
		t.Fatalf("InFlight = %d, want 2 (real outstanding count)", s.InFlight)
	}

	r1()
	if s := g.Stats(); s.InFlight != 1 {
		t.Fatalf("InFlight after late release = %d, want 1", s.InFlight)
	}
	r2()
	if s := g.Stats(); s.InFlight != 0 {
		t.Fatalf("InFlight = %d, want 0 after all late releases", s.InFlight)
	}
}

// 语义 4：release 重复调用（含并发）均无效，InFlight 永不为负。
func TestReleaseIdempotent(t *testing.T) {
	g := New(time.Now)

	rel, _ := g.Enter()
	rel()
	rel()
	rel()
	if s := g.Stats(); s.InFlight != 0 {
		t.Fatalf("InFlight = %d, want 0", s.InFlight)
	}

	const n = 200
	rels := make([]func(), n)
	for i := range rels {
		rels[i], _ = g.Enter()
	}
	var wg sync.WaitGroup
	for _, r := range rels {
		for range 4 {
			wg.Add(1)
			go func(fn func()) {
				defer wg.Done()
				fn()
			}(r)
		}
	}
	wg.Wait()

	s := g.Stats()
	if s.InFlight != 0 {
		t.Fatalf("InFlight = %d, want 0; count must never go negative", s.InFlight)
	}
	if s.Admitted != n+1 {
		t.Fatalf("Admitted = %d, want %d", s.Admitted, n+1)
	}
}
