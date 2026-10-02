package ontology

import (
	"errors"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

func TestNewEventQueueSkeleton(t *testing.T) {
	if _, err := NewEventQueue(1, 1, 1); err != nil {
		t.Fatalf("NewEventQueue() error = %v", err)
	}
}

func TestConstructorRejectsOutOfRangeConfig(t *testing.T) {
	invalid := [][3]int{
		{0, 1, 1},
		{1_000_001, 1, 1},
		{1, 0, 1},
		{1, 5, 1},
		{1, 1, 0},
		{1, 1, 1_000_001},
	}
	for _, cfg := range invalid {
		if _, err := NewEventQueue(cfg[0], cfg[1], cfg[2]); !errors.Is(err, ErrInvalid) {
			t.Fatalf("NewEventQueue(%v) error = %v, want ErrInvalid", cfg, err)
		}
	}
}

func TestLevelAndEdgeTriggerWaitRequeue(t *testing.T) {
	q, err := NewEventQueue(10, 1, 10)
	if err != nil {
		t.Fatal(err)
	}

	mustAdd(t, q, 0, 5, In, 0)
	mustAdd(t, q, 0, 6, In, ET)
	mustAdd(t, q, 0, 7, In|Out, 0)

	if err := q.SetState(5, In); err != nil {
		t.Fatal(err)
	}
	if err := q.SetState(6, In); err != nil {
		t.Fatal(err)
	}
	if err := q.SetState(7, Out); err != nil {
		t.Fatal(err)
	}
	assertQueue(t, q, 0, []int{5, 6, 7})

	events, err := q.Wait(0, 2)
	if err != nil {
		t.Fatal(err)
	}
	wantEvents := []Event{{Fd: 5, Revents: In}, {Fd: 6, Revents: In}}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("Wait(2) = %v, want %v", events, wantEvents)
	}
	assertQueue(t, q, 0, []int{7, 5})

	if err := q.SetState(6, 0); err != nil {
		t.Fatal(err)
	}
	if err := q.SetState(6, In); err != nil {
		t.Fatal(err)
	}
	assertQueue(t, q, 0, []int{7, 5, 6})

	events, err = q.Wait(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	wantEvents = []Event{
		{Fd: 7, Revents: Out},
		{Fd: 5, Revents: In},
		{Fd: 6, Revents: In},
	}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("Wait(10) = %v, want %v", events, wantEvents)
	}
	assertQueue(t, q, 0, []int{7, 5})

	if err := q.SetState(5, 0); err != nil {
		t.Fatal(err)
	}
	events, err = q.Wait(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	wantEvents = []Event{{Fd: 7, Revents: Out}}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("Wait after clear = %v, want %v", events, wantEvents)
	}
	assertQueue(t, q, 0, []int{7})
}

func TestEdgeTriggerRisingBits(t *testing.T) {
	q, err := NewEventQueue(10, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, q, 0, 1, In|Out, ET)

	if err := q.SetState(1, In); err != nil {
		t.Fatal(err)
	}
	assertQueue(t, q, 0, []int{1})
	if err := q.SetState(1, In); err != nil {
		t.Fatal(err)
	}
	assertQueue(t, q, 0, []int{1})

	if _, err := q.Wait(0, 1); err != nil {
		t.Fatal(err)
	}
	assertQueue(t, q, 0, nil)
	if err := q.SetState(1, In); err != nil {
		t.Fatal(err)
	}
	assertQueue(t, q, 0, nil)

	if err := q.SetState(1, 0); err != nil {
		t.Fatal(err)
	}
	if err := q.SetState(1, In); err != nil {
		t.Fatal(err)
	}
	assertQueue(t, q, 0, []int{1})
	if _, err := q.Wait(0, 1); err != nil {
		t.Fatal(err)
	}

	if err := q.SetState(1, In|Out); err != nil {
		t.Fatal(err)
	}
	assertQueue(t, q, 0, []int{1})
	events, err := q.Wait(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := events[0].Revents, In|Out; got != want {
		t.Fatalf("revents = %d, want %d", got, want)
	}
}

func TestOneShotRearmAndImplicitErrorHup(t *testing.T) {
	q, err := NewEventQueue(10, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, q, 0, 8, In, OneShot)
	if err := q.SetState(8, In); err != nil {
		t.Fatal(err)
	}

	events, err := q.Wait(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, []Event{{Fd: 8, Revents: In}}) {
		t.Fatalf("Wait = %v", events)
	}
	assertWatch(t, q, 8, Watch{Fd: 8, Interest: In, Flags: OneShot, Eff: In | Err | Hup, Disabled: true})

	if err := q.SetState(8, 0); err != nil {
		t.Fatal(err)
	}
	if err := q.SetState(8, In|Err); err != nil {
		t.Fatal(err)
	}
	assertQueue(t, q, 0, nil)

	if err := q.Mod(0, 8, In, OneShot); err != nil {
		t.Fatal(err)
	}
	assertQueue(t, q, 0, []int{8})
	assertWatch(t, q, 8, Watch{Fd: 8, Interest: In, Flags: OneShot, Eff: In | Err | Hup, Queued: true})

	events, err = q.Wait(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, []Event{{Fd: 8, Revents: In | Err}}) {
		t.Fatalf("Wait after rearm = %v", events)
	}

	mustAdd(t, q, 0, 9, 0, ET)
	if err := q.SetState(9, Hup); err != nil {
		t.Fatal(err)
	}
	events, err = q.Wait(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, []Event{{Fd: 9, Revents: Hup}}) {
		t.Fatalf("zero-interest Wait = %v", events)
	}
}

func TestInitialReadyAddAndModDoNotChooseExclusiveOwner(t *testing.T) {
	q, err := NewEventQueue(10, 2, 20)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.SetState(1, In); err != nil {
		t.Fatal(err)
	}

	mustAdd(t, q, 0, 1, In, ET|Excl)
	mustAdd(t, q, 1, 1, In, ET|Excl)
	assertQueue(t, q, 0, []int{1})
	assertQueue(t, q, 1, []int{1})

	if err := q.Del(0, 1); err != nil {
		t.Fatal(err)
	}
	if err := q.Mod(1, 1, Out, Excl); err != nil {
		t.Fatal(err)
	}
	assertQueue(t, q, 1, []int{1})
}

func TestModKeepsQueuePosition(t *testing.T) {
	q, err := NewEventQueue(10, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, q, 0, 1, In, 0)
	mustAdd(t, q, 0, 2, In, 0)
	if err := q.SetState(1, In); err != nil {
		t.Fatal(err)
	}
	if err := q.SetState(2, In); err != nil {
		t.Fatal(err)
	}
	if err := q.SetState(1, In|Out); err != nil {
		t.Fatal(err)
	}
	if err := q.Mod(0, 1, Out, 0); err != nil {
		t.Fatal(err)
	}
	assertQueue(t, q, 0, []int{1, 2})

	events, err := q.Wait(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, []Event{{Fd: 1, Revents: Out}, {Fd: 2, Revents: In}}) {
		t.Fatalf("Wait = %+v", events)
	}
}

func TestWaitDiscardsStaleAfterMaxAndRequeuesInReturnOrder(t *testing.T) {
	q, err := NewEventQueue(10, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, q, 0, 1, In, 0)
	mustAdd(t, q, 0, 2, In, 0)
	mustAdd(t, q, 0, 3, In, ET)
	mustAdd(t, q, 0, 4, In, 0)
	if err := q.SetState(1, In); err != nil {
		t.Fatal(err)
	}
	if err := q.SetState(2, In); err != nil {
		t.Fatal(err)
	}
	if err := q.SetState(3, In); err != nil {
		t.Fatal(err)
	}
	if err := q.SetState(4, In); err != nil {
		t.Fatal(err)
	}

	events, err := q.Wait(0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, []Event{{Fd: 1, Revents: In}, {Fd: 2, Revents: In}}) {
		t.Fatalf("Wait(2) = %+v", events)
	}
	assertQueue(t, q, 0, []int{3, 4, 1, 2})

	if err := q.SetState(1, 0); err != nil {
		t.Fatal(err)
	}
	events, err = q.Wait(0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, []Event{{Fd: 3, Revents: In}, {Fd: 4, Revents: In}}) {
		t.Fatalf("Wait(2) second = %+v", events)
	}
	assertQueue(t, q, 0, []int{1, 2, 4})

	events, err = q.Wait(0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, []Event{{Fd: 2, Revents: In}, {Fd: 4, Revents: In}}) {
		t.Fatalf("Wait(2) third = %+v", events)
	}
	assertQueue(t, q, 0, []int{2, 4})
}

func TestDelAndCloseRemoveFromAllQueues(t *testing.T) {
	q, err := NewEventQueue(10, 2, 20)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, q, 0, 1, In, 0)
	mustAdd(t, q, 0, 2, In, 0)
	mustAdd(t, q, 1, 1, In, 0)
	mustAdd(t, q, 1, 2, In, 0)
	for ep := 0; ep < 2; ep++ {
		if err := q.SetState(1, In); err != nil {
			t.Fatal(err)
		}
		if err := q.SetState(2, In); err != nil {
			t.Fatal(err)
		}
	}

	if err := q.Del(0, 1); err != nil {
		t.Fatal(err)
	}
	assertQueue(t, q, 0, []int{2})
	assertQueue(t, q, 1, []int{1, 2})

	removed, err := q.Close(2)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Fatalf("Close removed = %d, want 2", removed)
	}
	state, err := q.State(2)
	if err != nil {
		t.Fatal(err)
	}
	if state != 0 {
		t.Fatalf("State after Close = %d, want 0", state)
	}
	assertQueue(t, q, 0, nil)
	assertQueue(t, q, 1, []int{1})

	for ep := 0; ep < 2; ep++ {
		if err := q.Del(ep, 2); !errors.Is(err, ErrNoEnt) {
			t.Fatalf("Del after Close ep=%d error = %v, want ErrNoEnt", ep, err)
		}
	}
}

func TestExclusiveFanoutWakesLowestReadyCandidate(t *testing.T) {
	q, err := NewEventQueue(10, 2, 20)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, q, 0, 10, In, Excl)
	mustAdd(t, q, 1, 10, In, Excl)
	mustAdd(t, q, 0, 11, In, 0)
	mustAdd(t, q, 1, 11, In, 0)

	if err := q.SetState(10, In); err != nil {
		t.Fatal(err)
	}
	assertQueue(t, q, 0, []int{10})
	assertQueue(t, q, 1, nil)

	if _, err := q.Wait(0, 1); err != nil {
		t.Fatal(err)
	}
	assertQueue(t, q, 0, []int{10})

	if err := q.SetState(10, 0); err != nil {
		t.Fatal(err)
	}
	if err := q.SetState(10, In); err != nil {
		t.Fatal(err)
	}
	assertQueue(t, q, 0, []int{10})
	assertQueue(t, q, 1, []int{10})

	if err := q.SetState(11, In); err != nil {
		t.Fatal(err)
	}
	assertQueue(t, q, 0, []int{10, 11})
	assertQueue(t, q, 1, []int{10, 11})

	if removed, err := q.Close(10); err != nil || removed != 2 {
		t.Fatalf("Close(10) = (%d, %v), want 2, nil", removed, err)
	}
	assertQueue(t, q, 0, []int{11})
	assertQueue(t, q, 1, []int{11})
}

func TestExclusiveMixedCandidatesAndNoCandidate(t *testing.T) {
	q, err := NewEventQueue(10, 3, 30)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, q, 0, 1, In, ET|Excl)
	mustAdd(t, q, 1, 1, In, ET|Excl)
	mustAdd(t, q, 2, 1, In, ET)
	mustAdd(t, q, 0, 2, In, ET|Excl)

	if err := q.SetState(1, In); err != nil {
		t.Fatal(err)
	}
	assertQueue(t, q, 0, []int{1})
	assertQueue(t, q, 1, nil)
	assertQueue(t, q, 2, []int{1})

	if err := q.SetState(2, In); err != nil {
		t.Fatal(err)
	}
	assertQueue(t, q, 0, []int{1, 2})

	if err := q.SetState(1, 0); err != nil {
		t.Fatal(err)
	}
	if err := q.SetState(1, In); err != nil {
		t.Fatal(err)
	}
	assertQueue(t, q, 1, []int{1})

	assertQueue(t, q, 0, []int{1, 2})
	assertQueue(t, q, 1, []int{1})
}

func TestInstancesIndependent(t *testing.T) {
	q, err := NewEventQueue(10, 2, 20)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, q, 0, 1, In, OneShot)
	mustAdd(t, q, 1, 1, In, 0)
	if err := q.SetState(1, In); err != nil {
		t.Fatal(err)
	}

	if _, err := q.Wait(0, 1); err != nil {
		t.Fatal(err)
	}
	if err := q.Del(1, 1); err != nil {
		t.Fatal(err)
	}
	watches, err := q.Watches(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(watches) != 1 || !watches[0].Disabled {
		t.Fatalf("ep0 watches = %+v, want one disabled watch", watches)
	}
	if watches, err := q.Watches(1); err != nil || len(watches) != 0 {
		t.Fatalf("ep1 watches = %+v, err = %v", watches, err)
	}
}

func TestLimitsAndErrorOrder(t *testing.T) {
	q, err := NewEventQueue(1, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, q, 0, 1, In, 0)
	if err := q.Add(9, 1, In, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad ep error = %v", err)
	}
	if err := q.Add(0, 1, In, 0); !errors.Is(err, ErrExists) {
		t.Fatalf("exists error = %v", err)
	}
	if err := q.Add(0, 2, In, 0); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("per-instance limit error = %v", err)
	}
	mustAdd(t, q, 1, 2, In, 0)
	if err := q.Add(1, 3, In, 0); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("second per-instance limit error = %v", err)
	}

	q2, err := NewEventQueue(2, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, q2, 0, 1, In, 0)
	mustAdd(t, q2, 1, 2, In, 0)
	if err := q2.Add(0, 3, In, 0); !errors.Is(err, ErrTooMany) {
		t.Fatalf("global limit with instance space error = %v", err)
	}

	q3, err := NewEventQueue(2, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, q3, 0, 1, In, 0)
	mustAdd(t, q3, 1, 2, In, 0)
	mustAdd(t, q3, 0, 3, In, 0)
	if err := q3.Add(1, 4, In, 0); !errors.Is(err, ErrTooMany) {
		t.Fatalf("full global limit error = %v", err)
	}

	q4, err := NewEventQueue(3, 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	for _, fd := range []int{1, 2, 3} {
		mustAdd(t, q4, (fd-1)%2, fd, In, 0)
	}
	mustAdd(t, q4, 1, 4, In, 0)
	if err := q4.Add(0, 5, In, 0); !errors.Is(err, ErrTooMany) {
		t.Fatalf("limit after exact fill error = %v", err)
	}

	if err := q.Add(0, 3, In, OneShot|Excl); !errors.Is(err, ErrInvalid) {
		t.Fatalf("oneshot+excl error = %v", err)
	}
	if err := q.Add(0, 3, Err, Excl); !errors.Is(err, ErrInvalid) {
		t.Fatalf("excl ERR error = %v", err)
	}
	if err := q.Add(0, 3, Hup, Excl); !errors.Is(err, ErrInvalid) {
		t.Fatalf("excl HUP error = %v", err)
	}
	if err := q.Add(0, 3, In, 8); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown flags error = %v", err)
	}
	if err := q.Mod(9, 1, In, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Mod bad ep error = %v", err)
	}
	if err := q.Mod(0, 9, In, 0); !errors.Is(err, ErrNoEnt) {
		t.Fatalf("Mod missing error = %v", err)
	}
	if err := q.Mod(0, 1, In, Excl); !errors.Is(err, ErrExclChange) {
		t.Fatalf("Excl change error = %v", err)
	}

	state, err := q.State(1)
	if err != nil {
		t.Fatal(err)
	}
	if state != 0 {
		t.Fatalf("state after rejected ops = %d", state)
	}
	if err := q.Mod(0, 1, Out, 0); err != nil {
		t.Fatalf("Mod preserving EXCL failed: %v", err)
	}
}

func TestExaminedCounterIgnoresIdleWatches(t *testing.T) {
	q, err := NewEventQueue(1_000_000, 1, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}

	const idle = 100_000
	for fd := 1; fd <= idle; fd++ {
		mustAdd(t, q, 0, fd, In, 0)
	}
	mustAdd(t, q, 0, 0, In, 0)

	before := q.examined
	if err := q.SetState(0, In); err != nil {
		t.Fatal(err)
	}
	if got := q.examined - before; got != 1 {
		t.Fatalf("SetState examined = %d, want 1", got)
	}

	if err := q.SetState(0, In); err != nil {
		t.Fatal(err)
	}
	before = q.examined
	if events, err := q.Wait(0, 1); err != nil || len(events) != 1 {
		t.Fatalf("Wait = %#v, %v; want one event", events, err)
	}
	if got := q.examined - before; got != 1 {
		t.Fatalf("Wait examined = %d, want 1", got)
	}
}

func mustAdd(t *testing.T, q *EventQueue, ep, fd, interest, flags int) {
	t.Helper()
	if err := q.Add(ep, fd, interest, flags); err != nil {
		t.Fatalf("Add(%d, %d, %d, %d) error = %v", ep, fd, interest, flags, err)
	}
}

func assertQueue(t *testing.T, q *EventQueue, ep int, want []int) {
	t.Helper()
	got, err := q.Queue(ep)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		got = nil
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Queue(%d) = %v, want %v", ep, got, want)
	}
}

func assertWatch(t *testing.T, q *EventQueue, fd int, want Watch) {
	t.Helper()
	watches, err := q.Watches(0)
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range watches {
		if got.Fd == fd {
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("watch %d = %+v, want %+v", fd, got, want)
			}
			return
		}
	}
	t.Fatalf("watch %d not found", fd)
}

func TestConcurrentOperationsAreSafe(t *testing.T) {
	q, err := NewEventQueue(100, 4, 400)
	if err != nil {
		t.Fatal(err)
	}

	const goroutines = 16
	var wg sync.WaitGroup
	for worker := 0; worker < goroutines; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()

			rng := rand.New(rand.NewSource(int64(worker + 1000)))
			for step := 0; step < 300; step++ {
				ep := rng.Intn(4)
				fd := rng.Intn(40)
				switch rng.Intn(8) {
				case 0:
					_ = q.Add(ep, fd, rng.Intn(16), rng.Intn(8))
				case 1:
					_ = q.Mod(ep, fd, rng.Intn(16), rng.Intn(8))
				case 2:
					_ = q.Del(ep, fd)
				case 3:
					_, _ = q.Close(fd)
				case 4:
					_ = q.SetState(fd, rng.Intn(16))
				case 5:
					_, _ = q.Wait(ep, 1+rng.Intn(4))
				case 6:
					_, _ = q.Queue(ep)
				default:
					_, _ = q.Watches(ep)
				}
			}
		}(worker)
	}
	wg.Wait()

	q.mu.Lock()
	defer q.mu.Unlock()
	total := 0
	for ep := range q.tables {
		seen := map[*watch]bool{}
		for entry := q.queues[ep].head; entry != nil; entry = entry.next {
			if seen[entry] {
				t.Fatalf("duplicate queue entry in ep %d", ep)
			}
			seen[entry] = true
			if entry.ep != ep || !entry.Queued || entry.Disabled {
				t.Fatalf("invalid queued entry: %+v", entry.Watch)
			}
		}
		total += len(q.tables[ep])
		if len(q.tables[ep]) > q.perInst {
			t.Fatalf("ep %d has %d watches, limit %d", ep, len(q.tables[ep]), q.perInst)
		}
	}
	if total > q.global {
		t.Fatalf("total watches = %d, limit %d", total, q.global)
	}
}
