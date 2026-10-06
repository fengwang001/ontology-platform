package apf

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestConcurrency hammers the controller from many goroutines. Because all
// state transitions happen under one mutex, the outcome must equal some
// serial execution; the race detector plus the final consistency checks
// (no seats leaked, no waiter lost, occupancy never above nominal) verify
// that here.
func TestConcurrency(t *testing.T) {
	cfg := Config{
		TotalSeats: 8,
		Levels: []Level{
			{Name: "ex", Exempt: true},
			{Name: "L1", Shares: 3, QueueLimit: 1000, QueueTimeout: time.Hour},
			{Name: "L2", Shares: 1, QueueLimit: 1000, QueueTimeout: time.Hour},
		},
		Rules: []Rule{
			{Name: "admins", Precedence: 1, Users: []string{"admin"}, Level: "ex", DistinguishBy: ByUser},
			{Name: "reads", Precedence: 2, Verbs: []string{"get"}, Level: "L1", DistinguishBy: ByUser},
			{Name: "writes", Precedence: 3, Verbs: []string{"create"}, Level: "L2", DistinguishBy: ByNamespace},
		},
	}
	c, err := NewController(t0, cfg)
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	_, levels := c.DebugState()
	nominals := map[string]int{}
	for _, l := range levels {
		nominals[l.Name] = l.Nominal
	}
	// A shared monotone clock: every operation takes a unique tick, so no
	// operation ever wants for time. Ticks are handed out before the lock is
	// acquired, so an operation may still lose the race to a higher tick;
	// callers simply retry with a fresh tick, as a real concurrent caller
	// would.
	var tick int64
	now := func() time.Time {
		return t0.Add(time.Duration(atomic.AddInt64(&tick, 1)) * time.Millisecond)
	}
	admit := func(req Request) (*AdmitResult, error) {
		for {
			res, err := c.Admit(now(), req)
			if k, _ := KindOf(err); k == KindClockSkew {
				continue
			}
			return res, err
		}
	}
	finish := func(l *Lease) error {
		for {
			err := l.Finish(now())
			if k, _ := KindOf(err); k == KindClockSkew {
				continue
			}
			return err
		}
	}
	// Monitor: occupancy must never exceed nominal seats.
	var stopMonitor atomic.Bool
	var monitorWG sync.WaitGroup
	monitorWG.Add(1)
	go func() {
		defer monitorWG.Done()
		for !stopMonitor.Load() {
			_, levels := c.DebugState()
			for _, l := range levels {
				if l.Occupied > nominals[l.Name] {
					t.Errorf("level %s: occupied %d exceeds nominal %d", l.Name, l.Occupied, nominals[l.Name])
				}
			}
			time.Sleep(time.Millisecond)
		}
	}()
	const workers = 32
	const perWorker = 200
	var granted, finished, queued, timedOut, rejected atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			user := fmt.Sprintf("user-%d", id%8)
			for i := 0; i < perWorker; i++ {
				req := Request{User: user, Namespace: fmt.Sprintf("ns-%d", i%4), Seats: 1 + (i % 3)}
				switch i % 3 {
				case 0:
					req.Verb = "get"
				case 1:
					req.Verb = "create"
				default:
					req.User = "admin"
					req.Verb = "get"
				}
				res, err := admit(req)
				if err != nil {
					rejected.Add(1)
					continue
				}
				if res.Lease != nil {
					granted.Add(1)
					if err := finish(res.Lease); err != nil {
						t.Errorf("finish: %v", err)
					}
					finished.Add(1)
					continue
				}
				queued.Add(1)
				r := res.Ticket.Wait()
				if r.Err != nil {
					timedOut.Add(1)
					continue
				}
				granted.Add(1)
				if err := finish(r.Lease); err != nil {
					t.Errorf("finish: %v", err)
				}
				finished.Add(1)
			}
		}(w)
	}
	wg.Wait()
	stopMonitor.Store(true)
	monitorWG.Wait()
	if granted.Load() != finished.Load() {
		t.Fatalf("granted %d leases but finished %d", granted.Load(), finished.Load())
	}
	_, levels = c.DebugState()
	for _, l := range levels {
		if l.Occupied != 0 {
			t.Fatalf("level %s: %d seats leaked", l.Name, l.Occupied)
		}
		if l.Waiters != 0 {
			t.Fatalf("level %s: %d waiters lost", l.Name, l.Waiters)
		}
	}
	t.Logf("granted=%d finished=%d queued=%d timedOut=%d rejected=%d",
		granted.Load(), finished.Load(), queued.Load(), timedOut.Load(), rejected.Load())
	t.Log("判定依据: 互斥锁串行化; 占用席位永不超过名义席位; 无席位泄漏、无等待者丢失")
}
