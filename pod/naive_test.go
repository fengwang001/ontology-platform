package pod

// This file cross-checks the Pod state machine against a naive simulation
// written directly from the specification, over 2000 random event
// sequences. Every event logs its input, output and the rationale behind
// the naive decision (run with -v to see the log).

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// naivePod is a deliberately straightforward transcription of the spec.
type naiveContainer struct {
	state     string
	k         int64
	next      int64
	started   bool
	startedAt int64
}

type naivePod struct {
	cfg      Config
	cs       []naiveContainer
	restarts int64
	t0       int64
	t0set    bool
	maxNow   int64
}

func newNaivePod(cfg Config) *naivePod {
	n := &naivePod{
		cfg:    cfg,
		cs:     make([]naiveContainer, cfg.InitContainers+cfg.AppContainers),
		maxNow: -1,
	}
	for i := range n.cs {
		n.cs[i].state = "Waiting"
	}
	return n
}

func (n *naivePod) deadlineExceeded(now int64) bool {
	return n.cfg.ActiveDeadline > 0 && n.t0set && now-n.t0 >= n.cfg.ActiveDeadline
}

// start returns ("", rationale) on accept or (reason, rationale) on
// rejection.
func (n *naivePod) start(c int, now int64) (string, string) {
	total := n.cfg.InitContainers + n.cfg.AppContainers
	if c < 0 || c >= total || now < 0 {
		return "InvalidArgument", "container index out of range or now < 0"
	}
	if now < n.maxNow {
		return "ClockRegression", fmt.Sprintf("now=%d < maxNow=%d", now, n.maxNow)
	}
	co := &n.cs[c]
	if co.state != "Waiting" {
		return "NotWaiting", fmt.Sprintf("container %d is %s", c, co.state)
	}
	if n.deadlineExceeded(now) {
		return "DeadlineExceeded", fmt.Sprintf("now-t0=%d >= D=%d", now-n.t0, n.cfg.ActiveDeadline)
	}
	limit := n.cfg.InitContainers
	if c < n.cfg.InitContainers {
		limit = c
	}
	for i := 0; i < limit; i++ {
		if n.cs[i].state != "Succeeded" {
			return "InitNotComplete", fmt.Sprintf("init container %d is %s", i, n.cs[i].state)
		}
	}
	if now < co.next {
		return "Backoff", fmt.Sprintf("now=%d < next=%d", now, co.next)
	}
	co.state = "Running"
	co.startedAt = now
	co.started = true
	if !n.t0set {
		n.t0 = now
		n.t0set = true
	}
	n.maxNow = now
	return "", fmt.Sprintf("accepted: Running, startedAt=%d", now)
}

func (n *naivePod) exit(c int, code int, now int64) (string, string) {
	total := n.cfg.InitContainers + n.cfg.AppContainers
	if c < 0 || c >= total || now < 0 {
		return "InvalidArgument", "container index out of range or now < 0"
	}
	if now < n.maxNow {
		return "ClockRegression", fmt.Sprintf("now=%d < maxNow=%d", now, n.maxNow)
	}
	co := &n.cs[c]
	if co.state != "Running" {
		return "NotRunning", fmt.Sprintf("container %d is %s", c, co.state)
	}
	ran := now - co.startedAt
	isInit := c < n.cfg.InitContainers

	// Policy routing.
	restart := false
	switch {
	case isInit && code == 0:
		restart = false
	case isInit:
		restart = n.cfg.Policy == Always || n.cfg.Policy == OnFailure
	case code == 0:
		restart = n.cfg.Policy == Always
	default:
		restart = n.cfg.Policy == Always || n.cfg.Policy == OnFailure
	}

	kind := "init"
	if !isInit {
		kind = "app"
	}
	if restart && n.cfg.RestartBudget > 0 && n.restarts >= n.cfg.RestartBudget {
		// Budget exhausted: fall back to Never, no k/next/restarts change.
		if code == 0 {
			co.state = "Succeeded"
		} else {
			co.state = "Failed"
		}
		n.maxNow = now
		return "", fmt.Sprintf("%s container, policy restart, but budget exhausted (restarts=%d >= X=%d): Never fallback -> %s",
			kind, n.restarts, n.cfg.RestartBudget, co.state)
	}
	if restart {
		reset := ""
		if ran >= n.cfg.BackoffReset {
			co.k = 0
			reset = fmt.Sprintf("ran=%d >= R=%d: k reset to 0; ", ran, n.cfg.BackoffReset)
		}
		delay := naiveBackoff(n.cfg.BackoffBase, n.cfg.BackoffMax, co.k)
		co.next = now + delay
		co.k++
		n.restarts++
		co.state = "Waiting"
		n.maxNow = now
		return "", fmt.Sprintf("%s container, policy restart: %sdelay=min(B*2^k,M)=%d, next=%d, k=%d, restarts=%d",
			kind, reset, delay, co.next, co.k, n.restarts)
	}
	if code == 0 {
		co.state = "Succeeded"
	} else {
		co.state = "Failed"
	}
	n.maxNow = now
	return "", fmt.Sprintf("%s container, policy terminal (code=%d) -> %s", kind, code, co.state)
}

// naiveBackoff computes min(B*2^k, M) with iterative doubling and
// saturation, an independent formulation from the shift-based real one.
func naiveBackoff(base, max, k int64) int64 {
	delay := base
	for i := int64(0); i < k; i++ {
		if delay > max/2 {
			return max
		}
		delay *= 2
	}
	if delay > max {
		return max
	}
	return delay
}

func (n *naivePod) phase(now int64) (string, string) {
	if now < 0 {
		return "error", "now < 0"
	}
	if n.deadlineExceeded(now) {
		return "Failed", fmt.Sprintf("deadline: now-t0=%d >= D=%d", now-n.t0, n.cfg.ActiveDeadline)
	}
	for i := 0; i < n.cfg.InitContainers; i++ {
		if n.cs[i].state == "Failed" {
			return "Failed", fmt.Sprintf("init container %d Failed", i)
		}
	}
	allTerminal, anyFailed, anyStarted := true, false, false
	for i := n.cfg.InitContainers; i < len(n.cs); i++ {
		s := n.cs[i].state
		if s != "Succeeded" && s != "Failed" {
			allTerminal = false
		}
		if s == "Failed" {
			anyFailed = true
		}
		if n.cs[i].started {
			anyStarted = true
		}
	}
	if allTerminal {
		if anyFailed {
			return "Failed", "all app containers terminal, one Failed"
		}
		return "Succeeded", "all app containers Succeeded"
	}
	for i := 0; i < n.cfg.InitContainers; i++ {
		if n.cs[i].state != "Succeeded" {
			return "Pending", fmt.Sprintf("init container %d not Succeeded", i)
		}
	}
	if !anyStarted {
		return "Pending", "no app container ever started"
	}
	return "Running", "init done, some app container active"
}

func (n *naivePod) reason(c int, now int64) (string, string) {
	total := n.cfg.InitContainers + n.cfg.AppContainers
	if c < 0 || c >= total || now < 0 {
		return "error", "invalid argument"
	}
	co := &n.cs[c]
	terminal := co.state == "Succeeded" || co.state == "Failed"
	if !terminal && n.deadlineExceeded(now) {
		return "DeadlineExceeded", "non-terminal container past active deadline"
	}
	if co.state == "Waiting" && now < co.next {
		return "CrashLoopBackOff", fmt.Sprintf("Waiting and now=%d < next=%d", now, co.next)
	}
	return co.state, "state name"
}

// realRejectReason extracts the RejectReason string from a real Pod error.
func realRejectReason(err error) string {
	if err == nil {
		return ""
	}
	var pe *Error
	if errors.As(err, &pe) {
		return pe.Reason.String()
	}
	return "unexpected:" + err.Error()
}

func compareSnapshots(t *testing.T, seq, ev int, real Snapshot, n *naivePod) {
	t.Helper()
	fail := false
	if real.Restarts != n.restarts || real.T0Set != n.t0set || real.T0 != n.t0 {
		fail = true
	}
	for i := range real.Containers {
		rc, nc := real.Containers[i], n.cs[i]
		if rc.State.String() != nc.state || rc.K != nc.k || rc.Next != nc.next || rc.Started != nc.started {
			fail = true
		}
	}
	if fail {
		t.Fatalf("seq %d event %d: state divergence\n real=%+v\nnaive=%+v", seq, ev, real, n)
	}
}

func randomConfig(rng *rand.Rand) Config {
	cfg := Config{
		InitContainers: rng.Intn(4),
		AppContainers:  1 + rng.Intn(3),
		Policy:         Policy(rng.Intn(3)),
		BackoffBase:    1 + rng.Int63n(100),
		BackoffReset:   1 + rng.Int63n(500),
	}
	cfg.BackoffMax = cfg.BackoffBase + rng.Int63n(1000)
	if rng.Intn(2) == 0 {
		cfg.ActiveDeadline = 1 + rng.Int63n(3000)
	}
	if rng.Intn(10) < 7 {
		cfg.RestartBudget = int64(rng.Intn(6))
	}
	if rng.Intn(10) == 0 {
		// Occasionally exercise the overflow-safe backoff path.
		cfg.BackoffBase = 1_000_000_000_000
		cfg.BackoffMax = 1_000_000_000_000
	}
	return cfg
}

// TestNaiveCrossCheck replays 2000 random event sequences against both the
// real state machine and the naive simulation, comparing rejections,
// states, phases, reasons and next times after every single event.
func TestNaiveCrossCheck(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*7919 + 13))
		cfg := randomConfig(rng)
		real, err := New(cfg)
		if err != nil {
			t.Fatalf("seq %d: New(%+v): %v", seq, cfg, err)
		}
		naive := newNaivePod(cfg)
		total := cfg.InitContainers + cfg.AppContainers
		now := int64(0)
		events := 10 + rng.Intn(40)
		t.Logf("seq %d: config I=%d A=%d policy=%s B=%d M=%d R=%d D=%d X=%d, %d events",
			seq, cfg.InitContainers, cfg.AppContainers, cfg.Policy,
			cfg.BackoffBase, cfg.BackoffMax, cfg.BackoffReset,
			cfg.ActiveDeadline, cfg.RestartBudget, events)
		for ev := 0; ev < events; ev++ {
			// Advance time mostly monotonically, with occasional
			// regressions and rare negative values.
			switch r := rng.Intn(20); {
			case r < 14:
				now += rng.Int63n(60)
			case r < 17:
				// keep now
			case r < 19:
				now -= rng.Int63n(30)
			default:
				now = -1 - rng.Int63n(5)
			}
			c := rng.Intn(total+2) - 1 // -1..total: sometimes out of range
			code := rng.Intn(4) - 1    // -1..2

			switch rng.Intn(10) {
			case 0, 1, 2, 3: // Start
				realErr := real.Start(c, now)
				nReason, rationale := naive.start(c, now)
				got := realRejectReason(realErr)
				t.Logf("  ev %d: Start(c=%d, now=%d) => real=%q naive=%q (%s)",
					ev, c, now, got, nReason, rationale)
				if got != nReason {
					t.Fatalf("seq %d ev %d: Start(%d,%d) real=%q naive=%q", seq, ev, c, now, got, nReason)
				}
			case 4, 5, 6, 7: // Exit
				realErr := real.Exit(c, code, now)
				nReason, rationale := naive.exit(c, code, now)
				got := realRejectReason(realErr)
				t.Logf("  ev %d: Exit(c=%d, code=%d, now=%d) => real=%q naive=%q (%s)",
					ev, c, code, now, got, nReason, rationale)
				if got != nReason {
					t.Fatalf("seq %d ev %d: Exit(%d,%d,%d) real=%q naive=%q", seq, ev, c, code, now, got, nReason)
				}
			case 8: // Phase
				realPhase, realErr := real.Phase(now)
				nPhase, rationale := naive.phase(now)
				got := "error"
				if realErr == nil {
					got = realPhase.String()
				}
				t.Logf("  ev %d: Phase(now=%d) => real=%q naive=%q (%s)", ev, now, got, nPhase, rationale)
				if got != nPhase {
					t.Fatalf("seq %d ev %d: Phase(%d) real=%q naive=%q", seq, ev, now, got, nPhase)
				}
			default: // Reason
				realReason, realErr := real.Reason(c, now)
				nReason, rationale := naive.reason(c, now)
				got := "error"
				if realErr == nil {
					got = realReason
				}
				t.Logf("  ev %d: Reason(c=%d, now=%d) => real=%q naive=%q (%s)",
					ev, c, now, got, nReason, rationale)
				if got != nReason {
					t.Fatalf("seq %d ev %d: Reason(%d,%d) real=%q naive=%q", seq, ev, c, now, got, nReason)
				}
			}
			compareSnapshots(t, seq, ev, real.Snapshot(), naive)
		}
	}
}
