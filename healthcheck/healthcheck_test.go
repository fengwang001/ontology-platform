package healthcheck

import (
	"errors"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

func baseConfig() Config {
	return Config{
		N:                1,
		R:                2,
		F:                3,
		I:                10,
		FI:               3,
		DI:               20,
		InitiallyHealthy: true,
		Wf:               100,
		Q:                1,
	}
}

func mustNew(t *testing.T, cfg Config) *Checker {
	t.Helper()
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New(%+v): unexpected error %v", cfg, err)
	}
	return c
}

func mustProbe(t *testing.T, c *Checker, target int, ok bool, now int64) {
	t.Helper()
	if err := c.Probe(target, ok, now); err != nil {
		t.Fatalf("Probe(%d, %v, %d): unexpected error %v", target, ok, now, err)
	}
}

func expectProbeErr(t *testing.T, c *Checker, target int, ok bool, now int64, want error) {
	t.Helper()
	if err := c.Probe(target, ok, now); !errors.Is(err, want) {
		t.Fatalf("Probe(%d, %v, %d): got error %v, want %v", target, ok, now, err, want)
	}
}

func checkState(t *testing.T, c *Checker, target int, want Snapshot) {
	t.Helper()
	got, err := c.State(target)
	if err != nil {
		t.Fatalf("State(%d): unexpected error %v", target, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("State(%d) = %+v, want %+v", target, got, want)
	}
}

func checkHealthy(t *testing.T, c *Checker, want []int) {
	t.Helper()
	if got := c.Healthy(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Healthy() = %v, want %v", got, want)
	}
}

func TestConfigValidation(t *testing.T) {
	valid := baseConfig()
	mut := func(f func(*Config)) Config {
		c := valid
		f(&c)
		return c
	}
	invalid := []Config{
		mut(func(c *Config) { c.N = 0 }),
		mut(func(c *Config) { c.N = -3 }),
		mut(func(c *Config) { c.R = 0 }),
		mut(func(c *Config) { c.R = -1 }),
		mut(func(c *Config) { c.R = 1001 }),
		mut(func(c *Config) { c.F = 0 }),
		mut(func(c *Config) { c.I = 0 }),
		mut(func(c *Config) { c.I = 1_000_000_001 }),
		mut(func(c *Config) { c.FI = 0 }),
		mut(func(c *Config) { c.FI = 1_000_000_001 }),
		mut(func(c *Config) { c.DI = 0 }),
		mut(func(c *Config) { c.DI = 1_000_000_001 }),
		mut(func(c *Config) { c.Wf = 0 }),
		mut(func(c *Config) { c.Wf = 1_000_000_001 }),
		mut(func(c *Config) { c.Q = -1 }),
		mut(func(c *Config) { c.Q = c.N + 1 }),
	}
	for i, cfg := range invalid {
		if _, err := New(cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("invalid case %d: New(%+v) = %v, want %v", i, cfg, err, ErrInvalidConfig)
		}
	}
	validEdges := []Config{
		valid,
		mut(func(c *Config) { c.R = 1 }),
		mut(func(c *Config) { c.R = 1000 }),
		mut(func(c *Config) { c.F = 10_000 }),
		mut(func(c *Config) { c.I, c.FI, c.DI = 1, 1, 1 }),
		mut(func(c *Config) { c.I, c.FI, c.DI = 1_000_000_000, 1_000_000_000, 1_000_000_000 }),
		mut(func(c *Config) { c.Wf = 1 }),
		mut(func(c *Config) { c.Wf = 1_000_000_000 }),
		mut(func(c *Config) { c.Q = 0 }),
		mut(func(c *Config) { c.Q = c.N }),
	}
	for i, cfg := range validEdges {
		if _, err := New(cfg); err != nil {
			t.Errorf("valid case %d: New(%+v) = %v, want nil", i, cfg, err)
		}
	}
}

// TestWorkedExample replays the scenario from the specification.
func TestWorkedExample(t *testing.T) {
	cfg := baseConfig()
	cfg.N = 2
	c := mustNew(t, cfg)

	mustProbe(t, c, 0, false, 0)
	checkState(t, c, 0, Snapshot{Healthy: true, B: 1, Nd: 3, Fu: 3})

	mustProbe(t, c, 1, false, 1)
	checkState(t, c, 1, Snapshot{Healthy: true, B: 1, Nd: 11, Fu: 0})

	mustProbe(t, c, 0, false, 3)
	checkState(t, c, 0, Snapshot{Healthy: true, B: 2, Nd: 6, Fu: 6})

	mustProbe(t, c, 0, false, 6)
	checkState(t, c, 0, Snapshot{Healthy: false, Nd: 26, Fu: 0, Tr: []int64{6}})
	checkHealthy(t, c, []int{1})

	mustProbe(t, c, 0, true, 26)
	checkState(t, c, 0, Snapshot{Healthy: false, A: 1, Nd: 29, Fu: 29, Tr: []int64{6}})

	mustProbe(t, c, 0, true, 29)
	checkState(t, c, 0, Snapshot{Healthy: false, A: 2, Nd: 32, Fu: 32, Tr: []int64{6}})

	mustProbe(t, c, 0, true, 32)
	checkState(t, c, 0, Snapshot{Healthy: false, A: 3, Nd: 35, Fu: 35, Tr: []int64{6}})

	mustProbe(t, c, 0, true, 35)
	checkState(t, c, 0, Snapshot{Healthy: true, Nd: 45, Fu: 0, Tr: []int64{6, 35}})
	checkHealthy(t, c, []int{0, 1})
}

func TestR1F1ImmediateTransition(t *testing.T) {
	cfg := baseConfig()
	cfg.R, cfg.F = 1, 1
	c := mustNew(t, cfg)

	mustProbe(t, c, 0, false, 0)
	checkState(t, c, 0, Snapshot{Healthy: false, Nd: 20, Fu: 0, Tr: []int64{0}})

	// g=1 at now=20, so Reff=2: a single success does not recover yet.
	mustProbe(t, c, 0, true, 20)
	checkState(t, c, 0, Snapshot{Healthy: false, A: 1, Nd: 23, Fu: 23, Tr: []int64{0}})

	// At now=200 the transition expired, Reff=R=1: recovery is immediate.
	mustProbe(t, c, 0, true, 200)
	checkState(t, c, 0, Snapshot{Healthy: true, Nd: 210, Fu: 0, Tr: []int64{0, 200}})

	// R=1 with no prior flaps: one success recovers an unhealthy target.
	cfg.InitiallyHealthy = false
	c2 := mustNew(t, cfg)
	mustProbe(t, c2, 0, true, 0)
	checkState(t, c2, 0, Snapshot{Healthy: true, Nd: 10, Fu: 0, Tr: []int64{0}})
}

func TestHealthySuccessClearsB(t *testing.T) {
	c := mustNew(t, baseConfig())

	mustProbe(t, c, 0, false, 0)
	mustProbe(t, c, 0, false, 3)
	checkState(t, c, 0, Snapshot{Healthy: true, B: 2, Nd: 6, Fu: 6})

	mustProbe(t, c, 0, true, 6)
	checkState(t, c, 0, Snapshot{Healthy: true, B: 0, Nd: 16, Fu: 0})

	mustProbe(t, c, 0, false, 16)
	mustProbe(t, c, 0, false, 19)
	checkState(t, c, 0, Snapshot{Healthy: true, B: 2, Nd: 22, Fu: 22})

	mustProbe(t, c, 0, false, 22)
	checkState(t, c, 0, Snapshot{Healthy: false, Nd: 42, Fu: 0, Tr: []int64{22}})
}

func TestUnhealthyFailureClearsA(t *testing.T) {
	cfg := baseConfig()
	cfg.R, cfg.F = 3, 1
	c := mustNew(t, cfg)

	mustProbe(t, c, 0, false, 0)
	checkState(t, c, 0, Snapshot{Healthy: false, Nd: 20, Fu: 0, Tr: []int64{0}})

	mustProbe(t, c, 0, true, 20)
	mustProbe(t, c, 0, true, 23)
	checkState(t, c, 0, Snapshot{Healthy: false, A: 2, Nd: 26, Fu: 26, Tr: []int64{0}})

	mustProbe(t, c, 0, false, 26)
	checkState(t, c, 0, Snapshot{Healthy: false, A: 0, Nd: 46, Fu: 0, Tr: []int64{0}})

	mustProbe(t, c, 0, true, 46)
	checkState(t, c, 0, Snapshot{Healthy: false, A: 1, Nd: 49, Fu: 49, Tr: []int64{0}})
}

// TestFourIntervalSelections covers I, FI, DI and the unhealthy-FI branches.
func TestFourIntervalSelections(t *testing.T) {
	c := mustNew(t, baseConfig())

	// Healthy, b == 0 -> I.
	mustProbe(t, c, 0, true, 0)
	checkState(t, c, 0, Snapshot{Healthy: true, Nd: 10, Fu: 0})

	// Healthy, b > 0 -> FI (quota free).
	mustProbe(t, c, 0, false, 10)
	checkState(t, c, 0, Snapshot{Healthy: true, B: 1, Nd: 13, Fu: 13})

	// Transition probe selects by post-transition state: unhealthy, a == 0 -> DI.
	mustProbe(t, c, 0, false, 13)
	mustProbe(t, c, 0, false, 16)
	checkState(t, c, 0, Snapshot{Healthy: false, Nd: 36, Fu: 0, Tr: []int64{16}})

	// Unhealthy, a > 0 -> FI (quota free).
	mustProbe(t, c, 0, true, 36)
	checkState(t, c, 0, Snapshot{Healthy: false, A: 1, Nd: 39, Fu: 39, Tr: []int64{16}})

	// Recovery transition probe selects healthy, b == 0 -> I.
	mustProbe(t, c, 0, true, 39)
	mustProbe(t, c, 0, true, 42)
	mustProbe(t, c, 0, true, 45)
	checkState(t, c, 0, Snapshot{Healthy: true, Nd: 55, Fu: 0, Tr: []int64{16, 45}})
}

// TestFlapWindowBoundary: t+Wf == now is expired, t+Wf == now+1 still valid.
func TestFlapWindowBoundary(t *testing.T) {
	cfg := baseConfig()
	cfg.R, cfg.F = 2, 1
	cfg.I, cfg.FI, cfg.DI = 1, 1, 1
	cfg.Wf = 10

	prefix := func(c *Checker) {
		mustProbe(t, c, 0, false, 0) // tr=[0], unhealthy
		mustProbe(t, c, 0, true, 1)  // a=1
		mustProbe(t, c, 0, true, 2)  // a=2
	}

	// now=9: 0+10 > 9, g=1, Reff=4, a=3 < 4: no recovery.
	ca := mustNew(t, cfg)
	prefix(ca)
	mustProbe(t, ca, 0, true, 9)
	checkState(t, ca, 0, Snapshot{Healthy: false, A: 3, Nd: 10, Fu: 10, Tr: []int64{0}})

	// now=10: 0+10 > 10 is false, g=0, Reff=2, a=3 >= 2: recovers.
	cb := mustNew(t, cfg)
	prefix(cb)
	mustProbe(t, cb, 0, true, 10)
	checkState(t, cb, 0, Snapshot{Healthy: true, Nd: 11, Fu: 0, Tr: []int64{0, 10}})
}

// TestGCapAt4: more than 4 valid transitions count as 4.
func TestGCapAt4(t *testing.T) {
	cfg := baseConfig()
	cfg.R, cfg.F = 1, 1
	cfg.I, cfg.FI, cfg.DI = 1, 1, 1
	cfg.Wf = 1_000_000_000
	c := mustNew(t, cfg)

	mustProbe(t, c, 0, false, 0) // tr=[0]
	mustProbe(t, c, 0, true, 1)  // a=1 (Reff=2)
	mustProbe(t, c, 0, true, 2)  // recover, tr=[0,2]
	mustProbe(t, c, 0, false, 3) // tr=[0,2,3]
	for i := int64(4); i <= 7; i++ {
		mustProbe(t, c, 0, true, i) // g=3, Reff=4, recover at 7
	}
	mustProbe(t, c, 0, false, 8) // tr len 5
	for i := int64(9); i <= 13; i++ {
		mustProbe(t, c, 0, true, i) // g=4, Reff=5, recover at 13
	}
	mustProbe(t, c, 0, false, 14) // tr len 7
	// g=7 capped to 4 -> Reff=5: a=4 is not enough.
	for i := int64(15); i <= 18; i++ {
		mustProbe(t, c, 0, true, i)
	}
	checkState(t, c, 0, Snapshot{
		Healthy: false, A: 4, Nd: 19, Fu: 19,
		Tr: []int64{0, 2, 3, 7, 8, 13, 14},
	})
	mustProbe(t, c, 0, true, 19) // a=5 >= 5: recover
	got, _ := c.State(0)
	if !got.Healthy || len(got.Tr) != 8 {
		t.Fatalf("after capped Reff reached: %+v", got)
	}
}

// TestFlapDampingDelaysRecovery: a reaching the original R is not enough.
func TestFlapDampingDelaysRecovery(t *testing.T) {
	cfg := baseConfig()
	cfg.R, cfg.F = 3, 1
	cfg.I, cfg.FI, cfg.DI = 1, 1, 1
	c := mustNew(t, cfg)

	mustProbe(t, c, 0, false, 0) // tr=[0], Reff=3*(1+1)=6
	for now := int64(1); now <= 5; now++ {
		mustProbe(t, c, 0, true, now)
		got, _ := c.State(0)
		if got.Healthy {
			t.Fatalf("recovered too early at now=%d with a=%d (Reff=6)", now, got.A)
		}
	}
	checkState(t, c, 0, Snapshot{Healthy: false, A: 5, Nd: 6, Fu: 6, Tr: []int64{0}})
	mustProbe(t, c, 0, true, 6)
	checkState(t, c, 0, Snapshot{Healthy: true, Nd: 7, Fu: 0, Tr: []int64{0, 6}})
}

// TestReffDecreasesAfterExpiry: Reff drops as transitions leave the window.
func TestReffDecreasesAfterExpiry(t *testing.T) {
	cfg := baseConfig()
	cfg.R, cfg.F = 2, 1
	cfg.I, cfg.FI, cfg.DI = 1, 1, 1
	cfg.Wf = 10
	c := mustNew(t, cfg)

	mustProbe(t, c, 0, false, 0) // tr=[0]
	mustProbe(t, c, 0, true, 1)  // a=1, g=1, Reff=4
	// At now=11 the transition expired: g=0, Reff=2, a=2 >= 2 recovers.
	mustProbe(t, c, 0, true, 11)
	checkState(t, c, 0, Snapshot{Healthy: true, Nd: 12, Fu: 0, Tr: []int64{0, 11}})
}

// TestQ0NeverFast: with Q=0 the fast interval is never taken.
func TestQ0NeverFast(t *testing.T) {
	cfg := baseConfig()
	cfg.Q = 0
	c := mustNew(t, cfg)

	mustProbe(t, c, 0, false, 0) // b=1, candidate FI, x=0 >= Q=0 -> I
	checkState(t, c, 0, Snapshot{Healthy: true, B: 1, Nd: 10, Fu: 0})

	cfg.F = 1
	c2 := mustNew(t, cfg)
	mustProbe(t, c2, 0, false, 0) // unhealthy, a=0 -> DI
	checkState(t, c2, 0, Snapshot{Healthy: false, Nd: 20, Fu: 0, Tr: []int64{0}})
	mustProbe(t, c2, 0, true, 20) // a=1, candidate FI, quota full -> DI
	checkState(t, c2, 0, Snapshot{Healthy: false, A: 1, Nd: 40, Fu: 0, Tr: []int64{0}})
}

// TestQuotaBoundary: x == Q-1 takes FI, x == Q falls back to the regular interval.
func TestQuotaBoundary(t *testing.T) {
	cfg := baseConfig()
	cfg.N = 2
	c := mustNew(t, cfg)

	mustProbe(t, c, 0, false, 0) // x=0=Q-1 -> FI
	checkState(t, c, 0, Snapshot{Healthy: true, B: 1, Nd: 3, Fu: 3})

	mustProbe(t, c, 1, false, 1) // x=1=Q -> I
	checkState(t, c, 1, Snapshot{Healthy: true, B: 1, Nd: 11, Fu: 0})

	mustProbe(t, c, 1, false, 11) // target0 fu=3 expired -> x=0 -> FI
	checkState(t, c, 1, Snapshot{Healthy: true, B: 2, Nd: 14, Fu: 14})
}

// TestUnhealthyQuotaFallbackDI: unhealthy target with a>0 falls back to DI.
func TestUnhealthyQuotaFallbackDI(t *testing.T) {
	cfg := baseConfig()
	cfg.N = 2
	cfg.R, cfg.F = 5, 1
	c := mustNew(t, cfg)

	mustProbe(t, c, 0, false, 0)
	mustProbe(t, c, 1, false, 0)
	mustProbe(t, c, 0, true, 20) // a=1, x=0 -> FI, fu=23
	checkState(t, c, 0, Snapshot{Healthy: false, A: 1, Nd: 23, Fu: 23, Tr: []int64{0}})

	mustProbe(t, c, 1, true, 20) // a=1, x=1 >= Q -> DI
	checkState(t, c, 1, Snapshot{Healthy: false, A: 1, Nd: 40, Fu: 0, Tr: []int64{0}})
}

// TestOwnFuNotCounted: a target's own previous fu does not consume the quota.
func TestOwnFuNotCounted(t *testing.T) {
	c := mustNew(t, baseConfig())

	mustProbe(t, c, 0, false, 0)
	checkState(t, c, 0, Snapshot{Healthy: true, B: 1, Nd: 3, Fu: 3})

	// Own fu=3 is not > now=3 and would not count anyway: FI again.
	mustProbe(t, c, 0, false, 3)
	checkState(t, c, 0, Snapshot{Healthy: true, B: 2, Nd: 6, Fu: 6})
}

// TestProbeAtNdAllowed: now == nd is accepted, now == nd-1 is rejected.
func TestProbeAtNdAllowed(t *testing.T) {
	c := mustNew(t, baseConfig())

	mustProbe(t, c, 0, false, 0) // nd=3
	expectProbeErr(t, c, 0, false, 2, ErrProbeTooEarly)
	mustProbe(t, c, 0, false, 3)
	checkState(t, c, 0, Snapshot{Healthy: true, B: 2, Nd: 6, Fu: 6})
}

// TestClockRegression: now below the max accepted now is rejected even when
// the target's own nd has already been reached.
func TestClockRegression(t *testing.T) {
	cfg := baseConfig()
	cfg.N = 2
	c := mustNew(t, cfg)

	mustProbe(t, c, 0, true, 50) // maxNow=50, nd=60
	expectProbeErr(t, c, 1, true, 49, ErrClockRegression)
	mustProbe(t, c, 1, true, 50) // now == maxNow is allowed
	checkState(t, c, 1, Snapshot{Healthy: true, Nd: 60, Fu: 0})
	mustProbe(t, c, 1, true, 70)                          // maxNow=70
	expectProbeErr(t, c, 0, true, 55, ErrClockRegression) // also too early, regression first
	mustProbe(t, c, 0, true, 70)                          // nd=60 reached, now == maxNow
}

// TestRejectionOrder: reasons are reported in the specified priority order.
func TestRejectionOrder(t *testing.T) {
	c := mustNew(t, baseConfig())

	expectProbeErr(t, c, 1, true, -1, ErrTargetOutOfRange) // target before time
	expectProbeErr(t, c, -1, true, 0, ErrTargetOutOfRange)
	expectProbeErr(t, c, 0, true, -1, ErrInvalidTime)
	expectProbeErr(t, c, 0, true, 1_000_000_000_000_001, ErrInvalidTime)
	mustProbe(t, c, 0, true, 100)                         // nd=110, maxNow=100
	expectProbeErr(t, c, 0, true, 50, ErrClockRegression) // regression before too-early
	expectProbeErr(t, c, 0, true, 105, ErrProbeTooEarly)  // 105 >= maxNow, < nd
	mustProbe(t, c, 0, true, 1_000_000_000_000_000)       // boundary now is valid
}

func TestInitiallyUnhealthy(t *testing.T) {
	cfg := baseConfig()
	cfg.N = 2
	cfg.InitiallyHealthy = false
	c := mustNew(t, cfg)

	checkHealthy(t, c, []int{})
	checkState(t, c, 0, Snapshot{Healthy: false})

	mustProbe(t, c, 0, true, 0) // a=1, g=0, Reff=2, unhealthy a>0 -> FI
	checkState(t, c, 0, Snapshot{Healthy: false, A: 1, Nd: 3, Fu: 3})

	mustProbe(t, c, 0, true, 3) // a=2 >= 2 -> healthy, b=0 -> I
	checkState(t, c, 0, Snapshot{Healthy: true, Nd: 13, Fu: 0, Tr: []int64{3}})
	checkHealthy(t, c, []int{0})
	checkState(t, c, 1, Snapshot{Healthy: false})
}

// TestRejectedProbeKeepsState: rejected probes change nothing, including maxNow.
func TestRejectedProbeKeepsState(t *testing.T) {
	cfg := baseConfig()
	cfg.N = 2
	c := mustNew(t, cfg)

	mustProbe(t, c, 0, false, 0)  // b=1, nd=3, fu=3
	mustProbe(t, c, 1, true, 100) // nd=110, maxNow=100
	s0, _ := c.State(0)
	s1, _ := c.State(1)
	h := c.Healthy()

	expectProbeErr(t, c, 2, true, 0, ErrTargetOutOfRange)
	expectProbeErr(t, c, 0, true, -1, ErrInvalidTime)
	expectProbeErr(t, c, 0, true, 1_000_000_000_000_001, ErrInvalidTime)
	expectProbeErr(t, c, 0, true, 50, ErrClockRegression)
	expectProbeErr(t, c, 1, true, 105, ErrProbeTooEarly)

	checkState(t, c, 0, s0)
	checkState(t, c, 1, s1)
	checkHealthy(t, c, h)

	// maxNow unchanged: probing at 100 is still allowed.
	mustProbe(t, c, 0, true, 100)
}

func TestStateOnlyChecksTarget(t *testing.T) {
	c := mustNew(t, baseConfig())
	if _, err := c.State(-1); !errors.Is(err, ErrTargetOutOfRange) {
		t.Fatalf("State(-1) = %v, want %v", err, ErrTargetOutOfRange)
	}
	if _, err := c.State(1); !errors.Is(err, ErrTargetOutOfRange) {
		t.Fatalf("State(1) = %v, want %v", err, ErrTargetOutOfRange)
	}
	got, err := c.State(0)
	if err != nil {
		t.Fatalf("State(0): unexpected error %v", err)
	}
	want := Snapshot{Healthy: true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("State(0) = %+v, want %+v", got, want)
	}
}

// TestConcurrency hammers Probe/State/Healthy from many goroutines; run with
// -race. Results must be equivalent to some serial order, so no data races
// and no panics are the assertion here.
func TestConcurrency(t *testing.T) {
	cfg := baseConfig()
	cfg.N = 4
	cfg.Q = 2
	c := mustNew(t, cfg)

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g)))
			for i := 0; i < 500; i++ {
				target := rng.Intn(cfg.N)
				now := rng.Int63n(1000)
				_ = c.Probe(target, rng.Intn(2) == 0, now)
				if _, err := c.State(target); err != nil {
					t.Errorf("State(%d): %v", target, err)
				}
				if h := c.Healthy(); !sortIsSorted(h) {
					t.Errorf("Healthy() not sorted: %v", h)
				}
			}
		}(g)
	}
	wg.Wait()
}

func sortIsSorted(v []int) bool {
	for i := 1; i < len(v); i++ {
		if v[i] < v[i-1] {
			return false
		}
	}
	return true
}

// naiveModel is an independent step-by-step implementation of the rules,
// used as the reference for randomized differential testing.
type naiveModel struct {
	cfg    Config
	ts     []naiveTarget
	maxNow int64
}

type naiveTarget struct {
	healthy bool
	a       int64
	b       int64
	nd      int64
	fu      int64
	tr      []int64
}

func newNaiveModel(cfg Config) *naiveModel {
	m := &naiveModel{cfg: cfg, ts: make([]naiveTarget, cfg.N)}
	for i := range m.ts {
		m.ts[i].healthy = cfg.InitiallyHealthy
	}
	return m
}

func (m *naiveModel) probe(target int, ok bool, now int64) error {
	if target < 0 || target >= m.cfg.N {
		return ErrTargetOutOfRange
	}
	if now < 0 || now > 1_000_000_000_000_000 {
		return ErrInvalidTime
	}
	if now < m.maxNow {
		return ErrClockRegression
	}
	nt := &m.ts[target]
	if now < nt.nd {
		return ErrProbeTooEarly
	}
	if nt.healthy {
		if ok {
			nt.b = 0
		} else {
			nt.b++
			if nt.b >= m.cfg.F {
				nt.healthy = false
				nt.a, nt.b = 0, 0
				nt.tr = append(nt.tr, now)
			}
		}
	} else {
		if !ok {
			nt.a = 0
		} else {
			nt.a++
			var g int64
			for _, ts := range nt.tr {
				if ts+m.cfg.Wf > now {
					g++
				}
			}
			if g > 4 {
				g = 4
			}
			if nt.a >= m.cfg.R*(1+g) {
				nt.healthy = true
				nt.a, nt.b = 0, 0
				nt.tr = append(nt.tr, now)
			}
		}
	}
	fast := nt.healthy && nt.b > 0 || !nt.healthy && nt.a > 0
	var interval int64
	switch {
	case !fast && nt.healthy:
		interval = m.cfg.I
		nt.fu = 0
	case !fast:
		interval = m.cfg.DI
		nt.fu = 0
	default:
		x := 0
		for j := range m.ts {
			if j != target && m.ts[j].fu > now {
				x++
			}
		}
		if x >= m.cfg.Q {
			if nt.healthy {
				interval = m.cfg.I
			} else {
				interval = m.cfg.DI
			}
			nt.fu = 0
		} else {
			interval = m.cfg.FI
			nt.fu = now + m.cfg.FI
		}
	}
	nt.nd = now + interval
	m.maxNow = now
	return nil
}

func (m *naiveModel) snapshot(target int) Snapshot {
	nt := &m.ts[target]
	return Snapshot{
		Healthy: nt.healthy,
		A:       nt.a,
		B:       nt.b,
		Nd:      nt.nd,
		Fu:      nt.fu,
		Tr:      append([]int64(nil), nt.tr...),
	}
}

func (m *naiveModel) healthy() []int {
	out := []int{}
	for i := range m.ts {
		if m.ts[i].healthy {
			out = append(out, i)
		}
	}
	return out
}

type probeEvent struct {
	target int
	ok     bool
	now    int64
}

// TestRandomAgainstNaiveModel replays 2000 random probe sequences against
// both implementations and compares errors, states, tr and nd after every
// probe, plus the documented invariants.
func TestRandomAgainstNaiveModel(t *testing.T) {
	rng := rand.New(rand.NewSource(1121))
	for seq := 0; seq < 2000; seq++ {
		n := 1 + rng.Intn(5)
		cfg := Config{
			N:                n,
			R:                1 + rng.Int63n(5),
			F:                1 + rng.Int63n(5),
			I:                1 + rng.Int63n(20),
			FI:               1 + rng.Int63n(20),
			DI:               1 + rng.Int63n(20),
			InitiallyHealthy: rng.Intn(2) == 0,
			Wf:               1 + rng.Int63n(60),
			Q:                rng.Intn(n + 1),
		}
		c, err := New(cfg)
		if err != nil {
			t.Fatalf("seq %d: New(%+v): %v", seq, cfg, err)
		}
		m := newNaiveModel(cfg)

		count := 10 + rng.Intn(20)
		probes := make([]probeEvent, 0, count)
		for i := 0; i < count; i++ {
			target := rng.Intn(cfg.N)
			ok := rng.Intn(2) == 0
			base := m.maxNow
			if m.ts[target].nd > base {
				base = m.ts[target].nd
			}
			var now int64
			switch rng.Intn(10) {
			case 0, 1, 2, 3, 4:
				now = base + rng.Int63n(5) // usually acceptable
			case 5:
				now = m.ts[target].nd // exactly nd
			case 6:
				now = m.ts[target].nd - 1 // too early / regression / invalid
			case 7:
				now = m.maxNow - 1 // regression / invalid
			case 8:
				if rng.Intn(2) == 0 {
					now = -1
				} else {
					now = 1_000_000_000_000_001
				}
			default:
				now = rng.Int63n(m.maxNow + 10)
			}
			probes = append(probes, probeEvent{target: target, ok: ok, now: now})
		}
		t.Logf("seq=%d input cfg=%+v probes=%v", seq, cfg, probes)

		lastNd := make([]int64, cfg.N)
		for i, p := range probes {
			errC := c.Probe(p.target, p.ok, p.now)
			errM := m.probe(p.target, p.ok, p.now)
			if (errC == nil) != (errM == nil) || (errC != nil && !errors.Is(errC, errM)) {
				t.Fatalf("seq %d probe %d %+v: err mismatch: checker=%v model=%v", seq, i, p, errC, errM)
			}
			if errC != nil {
				continue
			}
			fastCount := 0
			for target := 0; target < cfg.N; target++ {
				got, err := c.State(target)
				if err != nil {
					t.Fatalf("seq %d probe %d: State(%d): %v", seq, i, target, err)
				}
				want := m.snapshot(target)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("seq %d probe %d %+v: State(%d)=%+v, model=%+v", seq, i, p, target, got, want)
				}
				if got.Healthy && got.A != 0 {
					t.Fatalf("seq %d probe %d: healthy target %d has a=%d", seq, i, target, got.A)
				}
				if !got.Healthy && got.B != 0 {
					t.Fatalf("seq %d probe %d: unhealthy target %d has b=%d", seq, i, target, got.B)
				}
				if target == p.target {
					if got.Nd <= p.now {
						t.Fatalf("seq %d probe %d: target %d nd=%d not after now=%d", seq, i, target, got.Nd, p.now)
					}
					if got.Nd <= lastNd[target] {
						t.Fatalf("seq %d probe %d: target %d nd=%d not increasing (last=%d)", seq, i, target, got.Nd, lastNd[target])
					}
					lastNd[target] = got.Nd
				}
				for k := 1; k < len(got.Tr); k++ {
					if got.Tr[k] < got.Tr[k-1] {
						t.Fatalf("seq %d probe %d: target %d tr not non-decreasing: %v", seq, i, target, got.Tr)
					}
				}
				if got.Fu > p.now {
					fastCount++
				}
			}
			if fastCount > cfg.Q {
				t.Fatalf("seq %d probe %d: %d targets hold fast leases at now=%d, Q=%d", seq, i, fastCount, p.now, cfg.Q)
			}
			if got, want := c.Healthy(), m.healthy(); !reflect.DeepEqual(got, want) {
				t.Fatalf("seq %d probe %d: Healthy()=%v, model=%v", seq, i, got, want)
			}
		}
		t.Logf("seq=%d output healthy=%v verdict=match (err+state+tr+nd compared after each of %d probes; invariants: a/b zeroing, nd increase, fast leases <= Q, tr non-decreasing)",
			seq, c.Healthy(), len(probes))
	}
}
