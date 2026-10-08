package vrrp

import (
	"math/rand"
	"sync"
	"testing"
)

// cluster is a tiny test harness that wires devices together: every
// advert a device emits is delivered to all peers as a received advert at
// the same timestamp, flooding until no device emits anything new. This
// demonstrates that multi-device behaviour is reproduced purely by
// feeding device outputs back into the other devices.
type cluster struct {
	t       *testing.T
	devices []*Device
}

type queuedAdvert struct {
	from int
	adv  Advert
}

func newCluster(t *testing.T, cfgs ...Config) *cluster {
	t.Helper()
	c := &cluster{t: t}
	for _, cfg := range cfgs {
		d, err := NewDevice(cfg)
		if err != nil {
			t.Fatalf("NewDevice(%+v): %v", cfg, err)
		}
		c.devices = append(c.devices, d)
	}
	return c
}

// send applies ev to device i and floods all emitted adverts through the
// cluster until quiescent. A rejected top-level event is returned to the
// caller; delivery failures during the flood are always fatal.
func (c *cluster) send(i int, ev Event) (Result, error) {
	c.t.Helper()
	res, err := c.devices[i].Handle(ev)
	if err != nil {
		return Result{}, err
	}
	queue := make([]queuedAdvert, 0, len(res.Adverts))
	for _, adv := range res.Adverts {
		queue = append(queue, queuedAdvert{from: i, adv: adv})
	}
	c.flood(queue, ev.Now)
	return res, nil
}

func (c *cluster) flood(queue []queuedAdvert, now uint64) {
	c.t.Helper()
	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]
		for j, d := range c.devices {
			if j == item.from {
				continue
			}
			if d.Snapshot().Role == RoleInit {
				continue // a stopped device drops the advert
			}
			out, err := d.Handle(ReceiveAdvert(item.adv, now))
			if err != nil {
				c.t.Fatalf("device %d receiving %v: %v", j, item.adv, err)
			}
			for _, adv := range out.Adverts {
				queue = append(queue, queuedAdvert{from: j, adv: adv})
			}
		}
	}
}

func (c *cluster) roles() []Role {
	roles := make([]Role, len(c.devices))
	for i, d := range c.devices {
		roles[i] = d.Snapshot().Role
	}
	return roles
}

func (c *cluster) masterCount() int {
	n := 0
	for _, d := range c.devices {
		if d.Snapshot().Role == RoleMaster {
			n++
		}
	}
	return n
}

// Two devices with no initial master both time out, both promote, and
// the exchanged adverts resolve the tie in favour of the higher
// priority device.
func TestClusterConvergesAfterSimultaneousPromotion(t *testing.T) {
	c := newCluster(t,
		Config{ID: "a", Priority: 200, Preempt: true, AdvertIntervalMs: 100},
		Config{ID: "b", Priority: 100, Preempt: true, AdvertIntervalMs: 100},
	)
	c.send(0, Start(0))
	c.send(1, Start(0))

	// Both watches expire at 300; both promote and their adverts cross.
	c.send(0, AdvanceTime(300))
	c.send(1, AdvanceTime(300))

	if got := c.roles(); got[0] != RoleMaster || got[1] != RoleBackup {
		t.Fatalf("roles = %v, want [master backup]", got)
	}

	// The master's periodic adverts keep the backup quiet.
	for now := uint64(400); now <= 900; now += 100 {
		c.send(0, TakeAdvert(now))
		c.send(1, AdvanceTime(now))
		if got := c.roles(); got[0] != RoleMaster || got[1] != RoleBackup {
			t.Fatalf("at %d: roles = %v, want [master backup]", now, got)
		}
	}
}

// An owner holds mastership while alive; when it stops, the
// zero-priority advert triggers the promotion wait and the
// highest-priority backup wins.
func TestClusterOwnerFailover(t *testing.T) {
	c := newCluster(t,
		Config{ID: "owner", Priority: 255, Preempt: true, AdvertIntervalMs: 100},
		Config{ID: "b1", Priority: 150, Preempt: true, AdvertIntervalMs: 100},
		Config{ID: "b2", Priority: 120, Preempt: true, AdvertIntervalMs: 100},
	)
	for i := range c.devices {
		c.send(i, Start(0))
	}
	if got := c.roles(); got[0] != RoleMaster || got[1] != RoleBackup || got[2] != RoleBackup {
		t.Fatalf("roles = %v, want [master backup backup]", got)
	}

	// Steady state: periodic owner adverts keep both backups quiet.
	for now := uint64(100); now <= 300; now += 100 {
		c.send(0, TakeAdvert(now))
		c.send(1, AdvanceTime(now))
		c.send(2, AdvanceTime(now))
	}
	if n := c.masterCount(); n != 1 {
		t.Fatalf("masters = %d, want 1", n)
	}

	// Owner stops at 400: the zero-priority advert arms the wait
	// (deadline 400 + 100/4 = 425) on both backups.
	c.send(0, Stop(400))
	c.send(1, AdvanceTime(424))
	c.send(2, AdvanceTime(424))
	if n := c.masterCount(); n != 0 {
		t.Fatalf("before wait expiry: masters = %d, want 0", n)
	}
	c.send(1, AdvanceTime(425))
	c.send(2, AdvanceTime(425))
	if got := c.roles(); got[1] != RoleMaster || got[2] != RoleBackup {
		t.Fatalf("after failover: roles = %v, want b1 master, b2 backup", got)
	}
}

// Randomized cluster: a single global clock drives random operations on
// three devices. As a real caller would, the harness polls every master
// for its due periodic advert each step; after the resulting flood
// quiesces, at most one master may remain.
func TestClusterRandomInvariant(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 200; trial++ {
		c := newCluster(t,
			Config{ID: "a", Priority: 1 + rng.Intn(254), Preempt: rng.Intn(2) == 0, AdvertIntervalMs: 10 + rng.Intn(40)},
			Config{ID: "b", Priority: 1 + rng.Intn(254), Preempt: rng.Intn(2) == 0, AdvertIntervalMs: 10 + rng.Intn(40)},
			Config{ID: "c", Priority: 1 + rng.Intn(254), Preempt: rng.Intn(2) == 0, AdvertIntervalMs: 10 + rng.Intn(40)},
		)
		now := uint64(0)
		for step := 0; step < 60; step++ {
			now += rng.Uint64() % 30
			i := rng.Intn(len(c.devices))
			var ev Event
			switch rng.Intn(10) {
			case 0:
				ev = Start(now)
			case 1:
				ev = Stop(now)
			case 2, 3:
				ev = TakeAdvert(now)
			case 4:
				ev = SetPriority(1+rng.Intn(254), now)
			case 5:
				ev = SetPreempt(rng.Intn(2) == 0, now)
			default:
				ev = AdvanceTime(now)
			}
			_, _ = c.send(i, ev) // rejections (e.g. not started) are fine

			// Poll every master for its periodic advert, advancing the
			// global clock to the due time when necessary.
			for j, d := range c.devices {
				if d.Snapshot().Role != RoleMaster {
					continue
				}
				at := now
				if due := d.Snapshot().NextAdvert; due > at {
					at = due
				}
				if at > now {
					now = at
				}
				if _, err := c.send(j, TakeAdvert(at)); err != nil {
					t.Fatalf("trial %d step %d: take advert: %v", trial, step, err)
				}
			}
			if n := c.masterCount(); n > 1 {
				t.Fatalf("trial %d step %d: %d masters after quiesce (roles %v)",
					trial, step, n, c.roles())
			}
		}
	}
}

// Concurrent Handle calls must behave as some serial order: no data
// races (run with -race) and the final clock equals the maximum accepted
// event time.
func TestConcurrentHandle(t *testing.T) {
	d := mustDevice(t, Config{ID: "self", Priority: 100, Preempt: true, AdvertIntervalMs: 50})

	const workers = 8
	const eventsPerWorker = 500

	var mu sync.Mutex
	maxAccepted := uint64(0)

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			now := uint64(0)
			for i := 0; i < eventsPerWorker; i++ {
				now += rng.Uint64() % 20
				var ev Event
				switch rng.Intn(6) {
				case 0:
					ev = Start(now)
				case 1:
					ev = Stop(now)
				case 2:
					ev = ReceiveAdvert(Advert{SenderID: "peer", Priority: rng.Intn(256), IntervalMs: 50}, now)
				case 3:
					ev = TakeAdvert(now)
				case 4:
					ev = SetPriority(1+rng.Intn(254), now)
				default:
					ev = AdvanceTime(now)
				}
				if _, err := d.Handle(ev); err == nil {
					mu.Lock()
					if ev.Now > maxAccepted {
						maxAccepted = ev.Now
					}
					mu.Unlock()
				}
			}
		}(int64(w))
	}
	wg.Wait()

	if got := d.Snapshot().Clock; got != maxAccepted {
		t.Fatalf("clock = %d, want max accepted time %d", got, maxAccepted)
	}
}

// The per-event cost must not grow with history length: after a long
// history, handling an event performs zero allocations.
func TestConstantCostPerEvent(t *testing.T) {
	d := mustDevice(t, Config{ID: "self", Priority: 100, Preempt: true, AdvertIntervalMs: MaxAdvertIntervalMs})
	mustHandle(t, d, Start(0))

	now := uint64(1)
	for i := 0; i < 100_000; i++ {
		mustHandle(t, d, AdvanceTime(now))
		now++
	}
	if got := d.Snapshot().Role; got != RoleBackup {
		t.Fatalf("role = %v, want backup (watch not expired)", got)
	}

	allocs := testing.AllocsPerRun(1000, func() {
		if _, err := d.Handle(AdvanceTime(now)); err != nil {
			t.Fatalf("Handle: %v", err)
		}
		now++
	})
	if allocs != 0 {
		t.Fatalf("Handle allocated %v times per event after long history, want 0", allocs)
	}
}

// A huge time jump is handled in constant time (no per-millisecond
// iteration): advancing by 2^40 ms in one event just works.
func TestHugeTimeJump(t *testing.T) {
	d := mustDevice(t, testConfig())
	mustHandle(t, d, Start(0))
	res := mustHandle(t, d, AdvanceTime(1<<40))
	mustAdverts(t, res, Advert{SenderID: "self", Priority: 100, IntervalMs: 100})
	if got := d.Snapshot().Role; got != RoleMaster {
		t.Fatalf("role = %v, want master", got)
	}
	if got := d.Snapshot().NextAdvert; got != (1<<40)+100 {
		t.Fatalf("next advert = %d, want %d", got, (1<<40)+100)
	}
}

func BenchmarkAdvanceTimeSmallJump(b *testing.B) {
	d, _ := NewDevice(Config{ID: "self", Priority: 100, Preempt: true, AdvertIntervalMs: MaxAdvertIntervalMs})
	_, _ = d.Handle(Start(0))
	now := uint64(1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = d.Handle(AdvanceTime(now))
		now++
	}
}

func BenchmarkAdvanceTimeHugeJump(b *testing.B) {
	d, _ := NewDevice(Config{ID: "self", Priority: 100, Preempt: true, AdvertIntervalMs: MaxAdvertIntervalMs})
	_, _ = d.Handle(Start(0))
	now := uint64(1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = d.Handle(AdvanceTime(now))
		now += 1 << 30
	}
}

func BenchmarkMasterTakeAdvert(b *testing.B) {
	d, _ := NewDevice(Config{ID: "owner", Priority: OwnerPriority, Preempt: true, AdvertIntervalMs: 100})
	_, _ = d.Handle(Start(0))
	now := uint64(0)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		now += 100
		_, _ = d.Handle(TakeAdvert(now))
	}
}
