package bbr

import (
	"sync"
	"testing"
)

func TestExampleSequence(t *testing.T) {
	c := newTestController(t)
	if got := c.Cwnd(); got != 10_000 {
		t.Fatalf("initial cwnd = %d, want 10000", got)
	}
	if got := c.Pacing(); got != 0 {
		t.Fatalf("initial pacing = %d, want 0", got)
	}

	mustAck(t, c, 0, 1000, 100, 0, 5000, false)
	if c.Round() != 1 || c.MaxBw() != 10_000 || c.MinRtt() != 100 {
		t.Fatalf("after first ack: round=%d maxBw=%d minRtt=%d", c.Round(), c.MaxBw(), c.MinRtt())
	}
	if c.Pacing() != 28_900 || c.Cwnd() != 4000 {
		t.Fatalf("after first ack: pacing=%d cwnd=%d", c.Pacing(), c.Cwnd())
	}

	mustAck(t, c, 50, 1000, 100, 0, 6000, false)
	if c.Round() != 1 || c.MaxBw() != 20_000 {
		t.Fatalf("after second ack: round=%d maxBw=%d", c.Round(), c.MaxBw())
	}

	mustAck(t, c, 100, 1000, 100, 1000, 6000, false)
	if c.Round() != 2 || c.MaxBw() != 20_000 {
		t.Fatalf("after third ack: round=%d maxBw=%d", c.Round(), c.MaxBw())
	}
	if c.Pacing() != 57_800 {
		t.Fatalf("pacing = %d, want 57800", c.Pacing())
	}
}

func TestLargeIntegerBDPAndWindow(t *testing.T) {
	if got := mulDivFloor(1_000_000_000_000_000_000, 5, 2); got != 2_500_000_000_000_000_000 {
		t.Fatalf("mulDivFloor() = %d, want 2500000000000000000", got)
	}

	c, err := New(1)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for i := 0; i < 1000; i++ {
		mustAck(t, c, 0, 1_000_000, 100_000, 0, 1_000_000_000_000, false)
	}
	if c.MaxBw() != 10_000_000 || c.MinRtt() != 100_000 {
		t.Fatalf("maxBw=%d minRtt=%d", c.MaxBw(), c.MinRtt())
	}
	if c.Pacing() != 28_900_000 || c.Cwnd() != 2_890_000_000 {
		t.Fatalf("startup pacing=%d cwnd=%d", c.Pacing(), c.Cwnd())
	}
}

func TestGrowthBoundaryAndStartupDrainToProbeBW(t *testing.T) {
	c := newTestController(t)
	mustAck(t, c, 0, 1000, 100, 0, 10_000, false)
	mustAck(t, c, 50, 1250, 100, 1000, 10_000, false)
	if c.State() != Startup {
		t.Fatalf("state after exactly 125%% growth = %s, want Startup", c.State())
	}

	mustAck(t, c, 100, 1000, 100, 2250, 10_000, false)
	mustAck(t, c, 150, 1000, 100, 3250, 10_000, false)
	if c.State() != Startup {
		t.Fatalf("state after two stalled rounds = %s, want Startup", c.State())
	}
	mustAck(t, c, 200, 1000, 100, 4250, 1000, false)
	if c.State() != ProbeBW {
		t.Fatalf("Startup -> Drain -> ProbeBW in one ack failed, state=%s", c.State())
	}
	if c.Pacing() != 12_500 || c.Cwnd() != 4000 {
		t.Fatalf("probe outputs: pacing=%d cwnd=%d", c.Pacing(), c.Cwnd())
	}
}

func TestAppLimitedFilter(t *testing.T) {
	c := newTestController(t)
	mustAck(t, c, 0, 1000, 100, 0, 5000, false)
	mustAck(t, c, 50, 1000, 100, 1000, 5000, false)

	before := c.FilterOps()
	mustAck(t, c, 60, 1, 100, 1999, 5000, true)
	if c.FilterOps() != before {
		t.Fatalf("small app-limited sample changed filterOps by %d", c.FilterOps()-before)
	}

	before = c.FilterOps()
	mustAck(t, c, 70, 1, 100, 1002, 5000, true)
	if c.FilterOps()-before != 2 {
		t.Fatalf("equal app-limited sample filterOps delta = %d, want 2", c.FilterOps()-before)
	}
	if c.MaxBw() != 10_000 {
		t.Fatalf("maxBw = %d, want 10000", c.MaxBw())
	}
}

func TestFilterWindowBoundary(t *testing.T) {
	c := newTestController(t)
	mustAck(t, c, 0, 1000, 100, 0, 1_000_000, false)
	ds := int64(1000)
	for r := int64(2); r <= 11; r++ {
		now := (r - 1) * 50
		mustAck(t, c, now, 1, 100, ds, 1_000_000, false)
		ds++
		want := int64(10_000)
		if r == 11 {
			want = 10
		}
		if c.Round() != r || c.MaxBw() != want {
			t.Fatalf("round %d: round=%d maxBw=%d want %d", r, c.Round(), c.MaxBw(), want)
		}
	}
}

func TestMinRTTRefreshAndExpiration(t *testing.T) {
	c := newTestController(t)
	mustAck(t, c, 0, 1000, 100, 0, 100_000, false)
	mustAck(t, c, 5000, 1, 100, 1000, 100_000, false)
	mustAck(t, c, 15_000, 1, 200, 1001, 100_000, false)
	if c.MinRtt() != 200 {
		t.Fatalf("minRtt = %d, want 200 after exact expiration", c.MinRtt())
	}
	if c.State() != ProbeRTT {
		t.Fatalf("state = %s, want ProbeRTT", c.State())
	}
}

func TestProbeBWAdvanceConditions(t *testing.T) {
	c := filledProbeController(t)

	for _, wantGain := range []int64{20_000, 20_000, 20_000, 20_000, 20_000, 20_000} {
		if c.Pacing() != wantGain {
			t.Fatalf("pacing before advance = %d, want %d", c.Pacing(), wantGain)
		}
		advanceProbeAck(t, c)
	}
	if c.Pacing() != 25_000 {
		t.Fatalf("pacing in 1.25 phase = %d, want 25000", c.Pacing())
	}

	now := c.cs + c.MinRtt()
	mustAck(t, c, now, 1, 100, c.d, 1000, false)
	if c.Pacing() != 25_000 {
		t.Fatalf("1.25 phase advanced without enough inflight, pacing=%d", c.Pacing())
	}
	mustAck(t, c, now, 1, 100, c.d, 3000, false)
	if c.Pacing() != 15_000 {
		t.Fatalf("pacing after 1.25 advance = %d, want 15000", c.Pacing())
	}
	mustAck(t, c, now+10, 1, 100, c.d, 1000, false)
	if c.Pacing() != 20_000 {
		t.Fatalf("0.75 phase did not advance on low inflight, pacing=%d", c.Pacing())
	}
}

func TestProbeRTTSameAckDeadlineAndExactExit(t *testing.T) {
	c := filledProbeController(t)
	mustAck(t, c, 10_200, 1, 100, c.d, 4000, false)
	if c.State() != ProbeRTT || c.Cwnd() != 4000 || c.Pacing() != 20_000 {
		t.Fatalf("entering ProbeRTT: state=%s cwnd=%d pacing=%d", c.State(), c.Cwnd(), c.Pacing())
	}
	mustAck(t, c, 10_399, 1, 100, c.d, 4000, false)
	if c.State() != ProbeRTT {
		t.Fatalf("state = %s before deadline", c.State())
	}
	mustAck(t, c, 10_400, 1, 100, c.d, 4000, false)
	if c.State() != ProbeBW {
		t.Fatalf("state = %s at exact deadline, want ProbeBW", c.State())
	}
	if c.MinRtt() != 100 || c.Pacing() != 20_000 {
		t.Fatalf("after exit minRtt=%d pacing=%d", c.MinRtt(), c.Pacing())
	}
}

func TestRejectedAcksDoNotMutateState(t *testing.T) {
	if _, err := New(0); err != ErrInvalidArgument {
		t.Fatalf("New(0) error = %v, want ErrInvalidArgument", err)
	}
	if _, err := New(65536); err != ErrInvalidArgument {
		t.Fatalf("New(65536) error = %v, want ErrInvalidArgument", err)
	}

	c := newTestController(t)
	if err := c.OnAck(-1, 1, 1, 0, 0, false); err != ErrInvalidArgument {
		t.Fatalf("invalid now error = %v", err)
	}
	if c.Round() != 0 || c.Cwnd() != 10_000 {
		t.Fatal("invalid argument changed state")
	}
	if err := c.OnAck(0, 1_000_001, 1, 0, 0, false); err != ErrInvalidArgument {
		t.Fatalf("invalid n error = %v", err)
	}

	mustAck(t, c, 10, 1000, 100, 0, 0, false)
	if err := c.OnAck(9, 1, 100, 0, 0, false); err != ErrClockRollback {
		t.Fatalf("rollback error = %v", err)
	}
	if err := c.OnAck(10, 1, 100, 1001, 0, false); err != ErrInvalidSample {
		t.Fatalf("invalid ds error = %v", err)
	}
	if err := c.OnAck(9, 1_000_001, 100, 1001, 0, false); err != ErrInvalidArgument {
		t.Fatalf("validation precedence error = %v", err)
	}
	if err := c.OnAck(10, 1, 100, 1001, 0, false); err != ErrInvalidSample {
		t.Fatalf("sample validation after legal clock = %v", err)
	}
	c.d = 999_999_999_999
	if err := c.OnAck(10, 2, 100, 0, 0, false); err != ErrInvalidArgument {
		t.Fatalf("D accumulation overflow error = %v", err)
	}
	if c.Round() != 1 || c.MaxBw() != 10_000 {
		t.Fatal("rejected ack changed accepted state")
	}
}

func TestFilterOpsBound(t *testing.T) {
	for _, acks := range []int{1000, 100_000} {
		t.Run("", func(t *testing.T) {
			c := newTestController(t)
			for i := 0; i < acks; i++ {
				now := int64(i)
				mustAck(t, c, now, 1, 100, c.d, 1_000_000, false)
			}
			if ops := c.FilterOps(); ops > int64(3*acks) {
				t.Fatalf("filterOps=%d exceeds 3*%d", ops, acks)
			}
		})
	}
}

func TestConcurrentQueries(t *testing.T) {
	c := newTestController(t)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = c.Pacing()
					_ = c.Cwnd()
					_ = c.MaxBw()
					_ = c.MinRtt()
					_ = c.Round()
					_ = c.State()
				}
			}
		}()
	}
	for i := 0; i < 500; i++ {
		mustAck(t, c, int64(i), 1, 100, c.d, 1_000_000, false)
	}
	close(stop)
	wg.Wait()
}

func mustAck(t *testing.T, c *Controller, now int64, n int64, rtt int64, ds int64, inflight int64, appLimited bool) {
	t.Helper()
	if err := c.OnAck(now, n, rtt, ds, inflight, appLimited); err != nil {
		t.Fatalf("OnAck(now=%d n=%d rtt=%d ds=%d inflight=%d app=%t): %v", now, n, rtt, ds, inflight, appLimited, err)
	}
}

func newTestController(t *testing.T) *Controller {
	t.Helper()
	c, err := New(1000)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func advanceProbeAck(t *testing.T, c *Controller) {
	t.Helper()
	now := c.cs + c.MinRtt()
	mustAck(t, c, now, 1, 100, c.d, 2000, false)
}

func filledProbeController(t *testing.T) *Controller {
	t.Helper()
	c := newTestController(t)
	mustAck(t, c, 0, 1000, 100, 0, 10_000, false)
	mustAck(t, c, 50, 1000, 50, 1000, 10_000, false)
	mustAck(t, c, 100, 1000, 50, 2000, 10_000, false)
	mustAck(t, c, 150, 1000, 50, 3000, 10_000, false)
	mustAck(t, c, 200, 1000, 50, 4000, 1000, false)
	if c.State() != ProbeBW {
		t.Fatalf("setup state = %s, want ProbeBW", c.State())
	}
	if c.MinRtt() != 50 {
		t.Fatalf("setup minRtt = %d, want 50", c.MinRtt())
	}
	return c
}
