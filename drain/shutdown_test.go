package drain

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 语义 5：Shutdown 幂等，多次（含并发）调用共享同一次等待与同一结果，
// 计数不因重复调用而变化。
func TestShutdownIdempotent(t *testing.T) {
	g := New(time.Now)
	rel, _ := g.Enter()

	deadline := time.Now().Add(30 * time.Millisecond)
	const callers = 16
	var (
		wg      sync.WaitGroup
		timeout int64
	)
	errs := make(chan error, callers)

	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- g.Shutdown(deadline)
		}()
	}

	// 停机后再发起的请求全部计入拒绝。
	g.mu.Lock()
	for !g.shuttingDown {
		g.mu.Unlock()
		time.Sleep(time.Millisecond)
		g.mu.Lock()
	}
	g.mu.Unlock()

	_, _ = g.Enter()
	_, _ = g.Enter()

	wg.Wait()
	close(errs)
	for err := range errs {
		if errors.Is(err, ErrDrainTimeout) {
			atomic.AddInt64(&timeout, 1)
		} else if err != nil {
			t.Fatalf("Shutdown = %v, want ErrDrainTimeout", err)
		}
	}
	if timeout != callers {
		t.Fatalf("timeout callers = %d, want %d (same shared result)", timeout, callers)
	}

	rel()
	s := g.Stats()
	if s.Admitted != 1 || s.Rejected != 2 {
		t.Fatalf("stats = %+v, want Admitted=1 Rejected=2", s)
	}
	if err := g.Shutdown(deadline); !errors.Is(err, ErrDrainTimeout) {
		t.Fatalf("repeat Shutdown = %v, want same ErrDrainTimeout", err)
	}
	if s := g.Stats(); s.Admitted != 1 || s.Rejected != 2 {
		t.Fatalf("stats after repeat shutdown = %+v, counts must not change", s)
	}
}

// 语义 6：Shutdown 等待期间归还的请求递减 InFlight，归零时立即唤醒。
func TestReleaseDuringShutdownWakes(t *testing.T) {
	g := New(time.Now)
	rels := make([]func(), 3)
	for i := range rels {
		rels[i], _ = g.Enter()
	}

	finished := make(chan error, 1)
	go func() { finished <- g.Shutdown(time.Now().Add(time.Hour)) }()

	for _, r := range rels {
		time.Sleep(10 * time.Millisecond)
		r()
	}

	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("Shutdown = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Shutdown not woken when in-flight reached zero")
	}
	if s := g.Stats(); s.InFlight != 0 || !s.Done {
		t.Fatalf("stats = %+v, want InFlight=0 Done=true", s)
	}
}

// Tick：注入时钟越过 deadline 时，等待中的 Shutdown 立即以超时结束。
func TestTickWithInjectedClock(t *testing.T) {
	now := time.Now()
	clock := &now
	g := New(func() time.Time { return *clock })

	rel, _ := g.Enter()

	finished := make(chan error, 1)
	go func() { finished <- g.Shutdown(now.Add(time.Hour)) }()

	time.Sleep(20 * time.Millisecond)
	g.Tick() // 时钟未推进，不应结束
	select {
	case <-finished:
		t.Fatal("Tick before deadline must not end shutdown")
	default:
	}

	*clock = now.Add(2 * time.Hour)
	g.Tick()
	select {
	case err := <-finished:
		if !errors.Is(err, ErrDrainTimeout) {
			t.Fatalf("Shutdown = %v, want ErrDrainTimeout", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Tick past deadline must end shutdown")
	}
	rel()
	if s := g.Stats(); s.InFlight != 0 {
		t.Fatalf("InFlight = %d, want 0", s.InFlight)
	}
}

// 语义 8：高并发下计数守恒且无竞态。
func TestConcurrentConservation(t *testing.T) {
	g := New(time.Now)

	const workers = 32
	const attempts = 500

	var admittedCount, attemptedCount int64
	var wg sync.WaitGroup

	stop := make(chan struct{})
	go func() {
		time.Sleep(20 * time.Millisecond)
		g.Shutdown(time.Now().Add(5 * time.Second))
		close(stop)
	}()

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range attempts {
				atomic.AddInt64(&attemptedCount, 1)
				rel, err := g.Enter()
				if err != nil {
					continue
				}
				atomic.AddInt64(&admittedCount, 1)
				rel()
			}
		}()
	}

	wg.Wait()
	<-stop

	s := g.Stats()
	if int64(s.Admitted) != admittedCount {
		t.Fatalf("Admitted=%d != successful Enters=%d", s.Admitted, admittedCount)
	}
	if int64(s.Admitted+s.Rejected) != attemptedCount {
		t.Fatalf("Admitted+Rejected=%d != attempts=%d", s.Admitted+s.Rejected, attemptedCount)
	}
	if s.InFlight != 0 {
		t.Fatalf("InFlight = %d, want 0 after all releases", s.InFlight)
	}
	if !s.Done {
		t.Fatal("Done = false, want true")
	}
}
