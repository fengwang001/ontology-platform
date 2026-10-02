package jit

import (
	"errors"
	"sync"
	"testing"
)

func testConfig() Config {
	return Config{
		Methods:             12,
		Tier1Calls:          3,
		Tier1MinCalls:       2,
		Tier1Total:          10,
		Tier2Calls:          6,
		Tier2MinCalls:       4,
		Tier2Total:          20,
		FeedbackDivisor:     2,
		QueueCapacity:       3,
		Tier1CompileTime:    5,
		Tier2CompileTime:    10,
		DecayPeriod:         100,
		DeoptCooldownBase:   30,
		Tier2DeoptThreshold: 2,
	}
}

func mustNewManager(t *testing.T, cfg Config) *Manager {
	t.Helper()
	manager, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	return manager
}

func call(t *testing.T, manager *Manager, method int64, backEdges int64, now int64) int {
	t.Helper()
	tier, err := manager.Call(method, backEdges, now)
	if err != nil {
		t.Fatalf("Call(%d,%d,%d) error = %v", method, backEdges, now, err)
	}
	return tier
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func requireErrorIs(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("error = %v, want %v", err, target)
	}
}

func requireEqual[T comparable](t *testing.T, name string, want, got T) {
	t.Helper()
	if want != got {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}

func requireJobs(t *testing.T, manager *Manager, want []Job) {
	t.Helper()
	if len(manager.queue) != len(want) {
		t.Fatalf("queue = %v, want %v", manager.queue, want)
	}
	for idx := range want {
		if manager.queue[idx] != want[idx] {
			t.Fatalf("queue[%d] = %v, want %v; full queue=%v", idx, manager.queue[idx], want[idx], manager.queue)
		}
	}
}

func queueMethodsAtTier1(t *testing.T, manager *Manager, methods []int64, startNow int64) {
	t.Helper()
	for offset, method := range methods {
		now := startNow + int64(offset*3)
		for attempt := 0; attempt < 3; attempt++ {
			call(t, manager, method, 0, now+int64(attempt))
		}
	}
}

func advanceQueue(t *testing.T, manager *Manager, now int64) {
	t.Helper()
	_, err := manager.Call(int64(manager.cfg.Methods-1), 0, now)
	requireNoError(t, err)
}

func putMethodAtTier(t *testing.T, manager *Manager, method int64, tier int) {
	t.Helper()
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.methods[method].tier = tier
	manager.methods[method].inFlight = false
}

func enqueueTestJob(manager *Manager, method int64, target int, start int64) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	duration := manager.cfg.Tier1CompileTime
	if target == 2 {
		duration = manager.cfg.Tier2CompileTime
	}
	if start < manager.lastFinish {
		start = manager.lastFinish
	}
	finish := start + duration
	manager.queue = append(manager.queue, Job{
		Method: method,
		Target: target,
		Start:  start,
		Finish: finish,
	})
	manager.methods[method].inFlight = true
	manager.lastFinish = finish
}

var _ = sync.WaitGroup{}

func TestSpecifiedTimeline(t *testing.T) {
	manager := mustNewManager(t, testConfig())

	requireEqual(t, "first call", 0, call(t, manager, 0, 0, 1))
	requireEqual(t, "second call", 0, call(t, manager, 0, 0, 2))
	requireEqual(t, "third call returns interpreter", 0, call(t, manager, 0, 0, 3))
	requireJobs(t, manager, []Job{{Method: 0, Target: 1, Start: 3, Finish: 8}})
	requireEqual(t, "in-flight call", 0, call(t, manager, 0, 0, 4))
	requireEqual(t, "exact install returns tier 1", 1, call(t, manager, 0, 0, 8))
	requireEqual(t, "next call at tier 1", 1, call(t, manager, 0, 0, 9))
	requireJobs(t, manager, []Job{{Method: 0, Target: 2, Start: 9, Finish: 19}})

	requireEqual(t, "method 1 first call", 0, call(t, manager, 1, 8, 10))
	requireEqual(t, "method 1 branch queues behind", 0, call(t, manager, 1, 8, 11))
	requireJobs(t, manager, []Job{
		{Method: 0, Target: 2, Start: 9, Finish: 19},
		{Method: 1, Target: 1, Start: 19, Finish: 24},
	})

	requireEqual(t, "method 2 call 12", 0, call(t, manager, 2, 0, 12))
	requireEqual(t, "method 2 call 13", 0, call(t, manager, 2, 0, 13))
	requireEqual(t, "scaled threshold not met", 0, call(t, manager, 2, 0, 14))
	requireEqual(t, "method 2 remains unqueued", 2, len(manager.queue))

	requireEqual(t, "install tier 2", 2, call(t, manager, 0, 0, 19))
	requireNoError(t, manager.Deopt(0, 20))
	requireEqual(t, "deopt tier", 0, manager.methods[0].tier)
	requireEqual(t, "deopt calls", Counter{}, manager.methods[0].calls)
	requireEqual(t, "deopt back edges", Counter{}, manager.methods[0].backEdges)
	requireEqual(t, "deopt count", int64(1), manager.methods[0].deopts)
	requireEqual(t, "cooldown until", int64(50), manager.methods[0].coolUntil)

	requireEqual(t, "cooldown call 21", 0, call(t, manager, 0, 0, 21))
	requireEqual(t, "cooldown call 22", 0, call(t, manager, 0, 0, 22))
	requireEqual(t, "cooldown call 23", 0, call(t, manager, 0, 0, 23))
	requireEqual(t, "no queue during cooldown", 1, len(manager.queue))

	requireEqual(t, "promotable exactly at cooldown end", 0, call(t, manager, 0, 0, 50))
	requireJobs(t, manager, []Job{
		{Method: 0, Target: 1, Start: 50, Finish: 55},
	})
	requireEqual(t, "method 1 installed while processing now=50", 1, manager.methods[1].tier)
}

func TestCompletionInstallsExactlyAtFinish(t *testing.T) {
	manager := mustNewManager(t, testConfig())
	call(t, manager, 0, 0, 1)
	call(t, manager, 0, 0, 2)
	tier, err := manager.Call(0, 0, 3)
	requireNoError(t, err)
	requireEqual(t, "call tier while queued", 0, tier)
	requireJobs(t, manager, []Job{{Method: 0, Target: 1, Start: 3, Finish: 8}})

	tier, err = manager.Call(0, 0, 8)
	requireNoError(t, err)
	requireEqual(t, "tier returned at exact finish", 1, tier)
	requireEqual(t, "installed tier", 1, manager.methods[0].tier)
	requireEqual(t, "queue length", 0, len(manager.queue))
	t.Logf("input=Call(0,n=0,now=8); install finish=8; reason=completed before execution; output=tier1")
}

func TestTier1ThresholdBranchesAtEquality(t *testing.T) {
	t.Run("calls branch", func(t *testing.T) {
		manager := mustNewManager(t, testConfig())
		call(t, manager, 0, 0, 1)
		call(t, manager, 0, 0, 2)
		tier := call(t, manager, 0, 0, 3)
		requireEqual(t, "returned tier", 0, tier)
		requireJobs(t, manager, []Job{{Method: 0, Target: 1, Start: 3, Finish: 8}})
		t.Logf("input=third call n=0; reason=i=3>=A1*s=3; output=enqueue tier1")
	})

	t.Run("back edge branch", func(t *testing.T) {
		manager := mustNewManager(t, testConfig())
		call(t, manager, 1, 9, 10)
		tier := call(t, manager, 1, 9, 11)
		requireEqual(t, "returned tier", 0, tier)
		requireJobs(t, manager, []Job{{Method: 1, Target: 1, Start: 11, Finish: 16}})
		t.Logf("input=n=9 twice; reason=i=2>=M1=2 and i+b=20>=B1=20; output=enqueue tier1")
	})
}

func TestTier2AndSkipAtEquality(t *testing.T) {
	t.Run("tier1 to tier2 back-edge branch", func(t *testing.T) {
		cfg := testConfig()
		cfg.Tier1Total = 100
		manager := mustNewManager(t, cfg)
		for now := int64(1); now <= 4; now++ {
			call(t, manager, 0, 4, now)
		}
		requireJobs(t, manager, []Job{{Method: 0, Target: 1, Start: 3, Finish: 8}})

		tier := call(t, manager, 0, 4, 8)
		requireEqual(t, "returned installed tier", 1, tier)
		requireJobs(t, manager, []Job{{Method: 0, Target: 2, Start: 8, Finish: 18}})
		tier = call(t, manager, 0, 0, 9)
		requireEqual(t, "returned tier before tier-2 install", 1, tier)
		requireJobs(t, manager, []Job{{Method: 0, Target: 2, Start: 8, Finish: 18}})
		t.Logf("input=install call makes i=5,b=20; reason=i>=M2=4 and i+b=20>=B2=20")
	})

	t.Run("direct skip when both H1 and H2", func(t *testing.T) {
		cfg := testConfig()
		cfg.Tier2Calls = 1
		cfg.Tier2MinCalls = 1
		manager := mustNewManager(t, cfg)
		tier := call(t, manager, 0, 0, 1)
		requireEqual(t, "returned tier", 0, tier)
		requireJobs(t, manager, []Job{{Method: 0, Target: 2, Start: 1, Finish: 11}})
		t.Logf("input=first call with A2=1; reason=H2 selected before H1; output=skip to tier2")
	})
}

func TestLoadScalingAndCapacity(t *testing.T) {
	cfg := testConfig()
	cfg.FeedbackDivisor = 3
	cfg.Tier1Calls = 3
	manager := mustNewManager(t, cfg)

	enqueueTestJob(manager, 1, 1, 10)
	enqueueTestJob(manager, 2, 1, 10)
	for now := int64(1); now <= 3; now++ {
		call(t, manager, 0, 0, now)
	}
	requireEqual(t, "q includes boundary enqueue", 3, len(manager.queue))
	t.Logf("input=q=2=F-1 at third decision; reason=s=1 and i=3>=A1; output=enqueue")

	manager = mustNewManager(t, cfg)
	manager.cfg.FeedbackDivisor = 100
	manager.cfg.Tier1Calls = 1
	manager.cfg.Tier1MinCalls = 1
	enqueueTestJob(manager, 1, 1, 10)
	enqueueTestJob(manager, 2, 1, 10)
	enqueueTestJob(manager, 3, 1, 10)
	tier := call(t, manager, 0, 0, 1)
	requireEqual(t, "return remains interpreted", 0, tier)
	requireEqual(t, "q=Qc drops job", int64(1), manager.discarded)
	t.Logf("input=i=3,b=9 with q=3=Qc,F=1; reason=threshold met and queue is full; output=discard")
}

func TestStartTimesRespectLFAndNow(t *testing.T) {
	manager := mustNewManager(t, testConfig())
	enqueueTestJob(manager, 1, 1, 2)
	call(t, manager, 0, 0, 1)
	call(t, manager, 0, 0, 2)
	call(t, manager, 0, 0, 3)
	requireJobs(t, manager, []Job{
		{Method: 1, Target: 1, Start: 2, Finish: 7},
		{Method: 0, Target: 1, Start: 7, Finish: 12},
	})
	t.Logf("input=promotion now=3 LF=7; reason=start=max(3,7)=7")

	advanceQueue(t, manager, 12)
	requireEqual(t, "all queued jobs installed", 0, len(manager.queue))
	call(t, manager, 2, 0, 20)
	call(t, manager, 2, 0, 21)
	call(t, manager, 2, 0, 22)
	requireJobs(t, manager, []Job{{Method: 2, Target: 1, Start: 22, Finish: 27}})
	t.Logf("input=promotion now=22 LF=12; reason=start=max(22,12)=22")
}

func TestDecayAcrossEpochsAndCap(t *testing.T) {
	cfg := testConfig()
	cfg.DecayPeriod = 10
	cfg.Tier1Calls = 1000
	cfg.Tier1MinCalls = 1000
	cfg.Tier1Total = 10000
	manager := mustNewManager(t, cfg)
	for _, now := range []int64{1, 2, 3} {
		call(t, manager, 2, 0, now)
	}
	enqueueTestJob(manager, 1, 1, 100)
	requireEqual(t, "queued before decay", 1, len(manager.queue))

	tier := call(t, manager, 2, 0, 20)
	requireEqual(t, "tier after decay call", 0, tier)
	requireEqual(t, "3 shifted once cumulatively then incremented", counterFromInt64(1), manager.methods[2].calls)
	requireEqual(t, "epoch advanced", int64(2), manager.methods[2].epoch)
	t.Logf("input=now=20 Pd=10; reason=i=3>>min(2,62)=0 then i++; output=i=1")

	manager.methods[2].calls = Counter{Lo: 1 << 62}
	manager.methods[2].epoch = 0
	call(t, manager, 2, 0, 1000)
	requireEqual(t, "right shift capped at 62 then incremented", counterFromInt64(2), manager.methods[2].calls)
}

func TestEpochBoundary(t *testing.T) {
	cfg := testConfig()
	cfg.DecayPeriod = 10
	manager := mustNewManager(t, cfg)
	call(t, manager, 0, 0, 9)
	call(t, manager, 0, 0, 10)
	requireEqual(t, "decay before boundary increment", counterFromInt64(1), manager.methods[0].calls)
	t.Logf("input=now=10 is epoch boundary; reason=old i=1 shifts to 0 then i++")
}

func TestDeoptCooldownPenaltyAndBan(t *testing.T) {
	manager := mustNewManager(t, testConfig())
	putMethodAtTier(t, manager, 0, 2)

	requireNoError(t, manager.Deopt(0, 20))
	requireEqual(t, "tier reset", 0, manager.methods[0].tier)
	requireEqual(t, "calls reset", Counter{}, manager.methods[0].calls)
	requireEqual(t, "back edges reset", Counter{}, manager.methods[0].backEdges)
	requireEqual(t, "deopt count", int64(1), manager.methods[0].deopts)
	requireEqual(t, "cooldown formula d=1", int64(50), manager.methods[0].coolUntil)

	for now := int64(21); now <= 23; now++ {
		call(t, manager, 0, 0, now)
	}
	requireEqual(t, "counts accumulate during cooldown", counterFromInt64(3), manager.methods[0].calls)
	requireEqual(t, "no promotion while now<cu", 0, len(manager.queue))

	call(t, manager, 0, 0, 50)
	requireJobs(t, manager, []Job{{Method: 0, Target: 1, Start: 50, Finish: 55}})
	t.Logf("input=now=cu=50 i=3; reason=cooldown allows equality; output=tier1 job")

	advanceQueue(t, manager, 55)
	putMethodAtTier(t, manager, 0, 2)
	requireNoError(t, manager.Deopt(0, 60))
	requireEqual(t, "linear penalty d=2", int64(120), manager.methods[0].coolUntil)

	call(t, manager, 0, 0, 120)
	call(t, manager, 0, 0, 121)
	call(t, manager, 0, 0, 122)
	requireEqual(t, "H1 remains usable after first d=2 cooldown", 1, len(manager.queue))
	advanceQueue(t, manager, 127)
	requireEqual(t, "reached tier 1", 1, manager.methods[0].tier)

	for now := int64(128); now <= 140; now++ {
		call(t, manager, 0, 0, now)
	}
	requireEqual(t, "banned tier 1 never promotes", 0, len(manager.queue))

	manager.mu.Lock()
	manager.methods[0].tier = 0
	manager.methods[0].calls = counterFromInt64(2)
	manager.methods[0].backEdges = counterFromInt64(8)
	manager.methods[0].coolUntil = 0
	manager.mu.Unlock()
	call(t, manager, 0, 0, 141)
	requireJobs(t, manager, []Job{{Method: 0, Target: 1, Start: 141, Finish: 146}})
	t.Logf("input=dc=Kd=2; reason=H2 banned but H1 unaffected; output=tier1 only")
}

func TestDeoptRejectsWrongState(t *testing.T) {
	manager := mustNewManager(t, testConfig())
	putMethodAtTier(t, manager, 0, 1)
	requireErrorIs(t, manager.Deopt(0, 1), ErrNotTier2)

	enqueueTestJob(manager, 1, 2, 10)
	requireErrorIs(t, manager.Deopt(1, 11), ErrNotTier2)
	requireEqual(t, "rejected deopt leaves inflight job", 1, len(manager.queue))
	requireEqual(t, "rejected deopt leaves flag", true, manager.methods[1].inFlight)
}

func TestRejectionOrderAndNoSideEffects(t *testing.T) {
	manager := mustNewManager(t, testConfig())
	enqueueTestJob(manager, 1, 1, 10)
	putMethodAtTier(t, manager, 0, 1)

	_, err := manager.Call(-1, 0, -1)
	requireErrorIs(t, err, ErrInvalidArgument)
	_, err = manager.Call(0, 1000001, 8)
	requireErrorIs(t, err, ErrInvalidArgument)
	_, err = manager.Call(0, 0, -1)
	requireErrorIs(t, err, ErrInvalidArgument)
	requireErrorIs(t, manager.Deopt(0, 8), ErrNotTier2)
	_, err = manager.Call(0, 0, 7)
	requireNoError(t, err)
	_, err = manager.Call(0, 0, 6)
	requireErrorIs(t, err, ErrClockRewound)

	requireJobs(t, manager, []Job{{Method: 1, Target: 1, Start: 10, Finish: 15}})
	requireEqual(t, "T advanced only by accepted call", int64(7), manager.now)
	requireEqual(t, "tier unchanged", 1, manager.methods[0].tier)
}

func TestStateIsInstallAndDecayViewOnly(t *testing.T) {
	manager := mustNewManager(t, testConfig())
	enqueueTestJob(manager, 0, 1, 3)
	manager.mu.Lock()
	manager.methods[0].calls = counterFromInt64(1)
	manager.methods[0].backEdges = counterFromInt64(3)
	manager.now = 9
	manager.mu.Unlock()
	requireJobs(t, manager, []Job{{Method: 0, Target: 1, Start: 3, Finish: 8}})

	_, err := manager.State(0, 8)
	requireErrorIs(t, err, ErrClockRewound)
	snapshot, err := manager.State(0, 9)
	requireNoError(t, err)
	requireEqual(t, "snapshot tier", 1, snapshot.Tier)
	requireEqual(t, "snapshot calls", counterFromInt64(1), snapshot.Calls)
	requireEqual(t, "snapshot back edges", counterFromInt64(3), snapshot.BackEdges)
	requireEqual(t, "snapshot queue", 0, snapshot.QueuedJobs)
	requireEqual(t, "snapshot LF", int64(8), snapshot.LastFinish)
	requireEqual(t, "real tier not installed", 0, manager.methods[0].tier)
	requireEqual(t, "real queue not drained", 1, len(manager.queue))

	_, err = manager.State(0, 7)
	requireErrorIs(t, err, ErrClockRewound)
	t.Logf("input=State(0,now=8); reason=hypothetical install finish<=8 then decay; output=no mutation")
}

func TestHeadChecksBoundedByInstalledPlusOne(t *testing.T) {
	manager := mustNewManager(t, testConfig())
	call(t, manager, 0, 0, 1)
	call(t, manager, 0, 0, 2)
	call(t, manager, 0, 0, 3)
	call(t, manager, 1, 0, 8)
	requireEqual(t, "install one then inspect next head", 2, manager.headChecks)
	call(t, manager, 1, 0, 9)
	requireEqual(t, "empty queue checks one sentinel", 1, manager.headChecks)
}

func TestConcurrentCalls(t *testing.T) {
	manager := mustNewManager(t, testConfig())
	const goroutines = 12
	const iterations = 80
	var wait sync.WaitGroup
	for goroutine := 0; goroutine < goroutines; goroutine++ {
		wait.Add(1)
		go func(id int) {
			defer wait.Done()
			for step := 0; step < iterations; step++ {
				now := int64(id*iterations + step + 1)
				_, _ = manager.Call(int64(id), 0, now)
			}
		}(goroutine)
	}
	wait.Wait()

	manager.mu.Lock()
	defer manager.mu.Unlock()
	if len(manager.queue) > int(manager.cfg.QueueCapacity) {
		t.Fatalf("queue length %d exceeds capacity %d", len(manager.queue), manager.cfg.QueueCapacity)
	}
	seen := map[int64]bool{}
	var previousFinish int64
	for _, job := range manager.queue {
		if seen[job.Method] {
			t.Fatalf("method %d has duplicate inflight jobs", job.Method)
		}
		seen[job.Method] = true
		if !manager.methods[job.Method].inFlight {
			t.Fatalf("queued job %+v missing inflight flag", job)
		}
		if job.Finish < previousFinish {
			t.Fatalf("finish times not monotonic: %d after %d", job.Finish, previousFinish)
		}
		previousFinish = job.Finish
	}
}
