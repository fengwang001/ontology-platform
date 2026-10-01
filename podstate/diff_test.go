package podstate

import (
	"math/rand"
	"testing"
)

// naivePod is a from-scratch reimplementation of the spec, written
// independently of the production code for differential testing.
type naivePod struct {
	i, a          int
	policy        int
	b, m, r, d, x int64
	state         []int // 0 waiting 1 running 2 succeeded 3 failed
	k             []int
	next          []int64
	started       []bool
	startedAt     []int64
	restarts      int64
	t0            int64
	hasT0         bool
	maxNow        int64
}

func newNaive(cfg Config) *naivePod {
	n := cfg.InitContainers + cfg.AppContainers
	return &naivePod{
		i: cfg.InitContainers, a: cfg.AppContainers,
		policy: int(cfg.RestartPolicy),
		b:      cfg.BackoffBase, m: cfg.BackoffMax, r: cfg.BackoffReset,
		d: cfg.ActiveDeadline, x: cfg.RestartBudget,
		state:     make([]int, n),
		k:         make([]int, n),
		next:      make([]int64, n),
		started:   make([]bool, n),
		startedAt: make([]int64, n),
	}
}

func stateName(s int) string {
	return []string{"Waiting", "Running", "Succeeded", "Failed"}[s]
}

func nbool(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func nitoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [24]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func (n *naivePod) delay(k int) int64 {
	d := n.b
	for j := 0; j < k; j++ {
		if d >= n.m || d >= 1<<62 {
			return n.m
		}
		d <<= 1
		if d >= n.m {
			return n.m
		}
	}
	if d > n.m {
		return n.m
	}
	return d
}

// start/exit/phase/reason return (result, basis) where basis explains the
// decision and is printed in the test log.
func (n *naivePod) start(c int, now int64) (string, string) {
	if c < 0 || c >= n.i+n.a || now < 0 {
		return "reject:invalid-argument", "bad c or now<0"
	}
	if now < n.maxNow {
		return "reject:clock-went-back", "maxNow=" + nitoa(n.maxNow)
	}
	if n.state[c] != 0 {
		return "reject:start-not-waiting", "state=" + stateName(n.state[c])
	}
	if n.hasT0 && n.d > 0 && now-n.t0 >= n.d {
		return "reject:deadline-exceeded", "elapsed=" + nitoa(now-n.t0) + " D=" + nitoa(n.d)
	}
	if c < n.i {
		for j := 0; j < c; j++ {
			if n.state[j] != 2 {
				return "reject:init-not-complete", "init " + nitoa(int64(j)) + " " + stateName(n.state[j])
			}
		}
	} else {
		for j := 0; j < n.i; j++ {
			if n.state[j] != 2 {
				return "reject:init-not-complete", "init " + nitoa(int64(j)) + " " + stateName(n.state[j])
			}
		}
	}
	if now < n.next[c] {
		return "reject:backing-off", "now=" + nitoa(now) + " next=" + nitoa(n.next[c])
	}
	n.state[c] = 1
	n.startedAt[c] = now
	n.started[c] = true
	if !n.hasT0 {
		n.hasT0 = true
		n.t0 = now
	}
	n.maxNow = now
	return "accept", "running startedAt=" + nitoa(now)
}

func (n *naivePod) exit(c int, code, now int64) (string, string) {
	if c < 0 || c >= n.i+n.a || now < 0 {
		return "reject:invalid-argument", "bad c or now<0"
	}
	if now < n.maxNow {
		return "reject:clock-went-back", "maxNow=" + nitoa(n.maxNow)
	}
	if n.state[c] != 1 {
		return "reject:exit-not-running", "state=" + stateName(n.state[c])
	}
	ran := now - n.startedAt[c]
	isInit := c < n.i
	restart := false
	switch {
	case code == 0 && isInit:
		n.state[c] = 2
	case code != 0 && isInit && (n.policy == 0 || n.policy == 1):
		restart = true
	case code != 0 && isInit:
		n.state[c] = 3
	case code == 0 && !isInit && n.policy == 0:
		restart = true
	case code == 0 && !isInit:
		n.state[c] = 2
	case code != 0 && !isInit && (n.policy == 0 || n.policy == 1):
		restart = true
	default:
		n.state[c] = 3
	}
	basis := "policy-route ran=" + nitoa(ran)
	if restart && n.x > 0 && n.restarts >= n.x {
		if code == 0 {
			n.state[c] = 2
			basis = "budget-exhausted:zero->Succeeded"
		} else {
			n.state[c] = 3
			basis = "budget-exhausted:nonzero->Failed"
		}
		restart = false
	}
	if restart {
		reset := false
		if ran >= n.r {
			n.k[c] = 0
			reset = true
		}
		d := n.delay(n.k[c])
		n.next[c] = now + d
		n.k[c]++
		n.restarts++
		n.state[c] = 0
		basis = "restart ran=" + nitoa(ran) + " reset=" + nbool(reset) +
			" delay=" + nitoa(d) + " next=" + nitoa(n.next[c]) +
			" k=" + nitoa(int64(n.k[c])) + " restarts=" + nitoa(n.restarts)
	}
	n.maxNow = now
	return "accept", basis + " -> " + stateName(n.state[c])
}

func (n *naivePod) phase(now int64) (string, string) {
	if n.d > 0 && n.hasT0 && now-n.t0 >= n.d {
		return "Failed", "deadline elapsed=" + nitoa(now-n.t0)
	}
	for j := 0; j < n.i; j++ {
		if n.state[j] == 3 {
			return "Failed", "init " + nitoa(int64(j)) + " Failed"
		}
	}
	allTerminal, anyFailed, anyStarted := true, false, false
	for j := n.i; j < n.i+n.a; j++ {
		if n.state[j] != 2 && n.state[j] != 3 {
			allTerminal = false
		}
		if n.state[j] == 3 {
			anyFailed = true
		}
		if n.started[j] {
			anyStarted = true
		}
	}
	if allTerminal {
		if anyFailed {
			return "Failed", "all apps terminal, some Failed"
		}
		return "Succeeded", "all apps Succeeded"
	}
	for j := 0; j < n.i; j++ {
		if n.state[j] != 2 {
			return "Pending", "init " + nitoa(int64(j)) + " not Succeeded"
		}
	}
	if !anyStarted {
		return "Pending", "no app ever started"
	}
	return "Running", "apps in progress"
}

func (n *naivePod) reason(c int, now int64) (string, string) {
	if c < 0 || c >= n.i+n.a || now < 0 {
		return "reject:invalid-argument", "bad c or now<0"
	}
	st := n.state[c]
	if st != 2 && st != 3 && n.d > 0 && n.hasT0 && now-n.t0 >= n.d {
		return "DeadlineExceeded", "deadline elapsed=" + nitoa(now-n.t0)
	}
	if st == 0 && now < n.next[c] {
		return "CrashLoopBackOff", "now=" + nitoa(now) + " next=" + nitoa(n.next[c])
	}
	return stateName(st), "state"
}

func errName(err error) string {
	if err == nil {
		return "accept"
	}
	if re, ok := asReject(err); ok {
		switch re.Reason {
		case RejectInvalidArgument:
			return "reject:invalid-argument"
		case RejectClockWentBack:
			return "reject:clock-went-back"
		case RejectStartNotWaiting:
			return "reject:start-not-waiting"
		case RejectExitNotRunning:
			return "reject:exit-not-running"
		case RejectDeadlineExceeded:
			return "reject:deadline-exceeded"
		case RejectInitNotComplete:
			return "reject:init-not-complete"
		case RejectBackingOff:
			return "reject:backing-off"
		}
	}
	return "reject:unknown"
}

func snapshotsEqual(t *testing.T, p *Pod, n *naivePod) bool {
	t.Helper()
	total := n.i + n.a
	for c := 0; c < total; c++ {
		s, err := p.Container(c)
		if err != nil {
			t.Errorf("snapshot container %d: %v", c, err)
			return false
		}
		if s.State.String() != stateName(n.state[c]) ||
			s.Fails != n.k[c] || s.Next != n.next[c] || s.Started != n.started[c] {
			t.Errorf("container %d differs: impl{state=%s k=%d next=%d started=%v} naive{state=%s k=%d next=%d started=%v}",
				c, s.State, s.Fails, s.Next, s.Started,
				stateName(n.state[c]), n.k[c], n.next[c], n.started[c])
			return false
		}
	}
	if p.Restarts() != n.restarts {
		t.Errorf("restarts differ: impl=%d naive=%d", p.Restarts(), n.restarts)
		return false
	}
	t0, ok := p.T0()
	if ok != n.hasT0 || (ok && t0 != n.t0) {
		t.Errorf("t0 differ: impl=%d,%v naive=%d,%v", t0, ok, n.t0, n.hasT0)
		return false
	}
	return true
}

func TestRandomDifferential2000(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))
	for iter := 0; iter < 2000; iter++ {
		cfg := Config{
			InitContainers: rng.Intn(4),
			AppContainers:  1 + rng.Intn(3),
			RestartPolicy:  RestartPolicy(rng.Intn(3)),
			BackoffBase:    int64(1 + rng.Intn(5)),
			BackoffReset:   int64(2 + rng.Intn(20)),
			RestartBudget:  int64(rng.Intn(4)),
		}
		cfg.BackoffMax = cfg.BackoffBase * int64(1+rng.Intn(4))
		if rng.Intn(2) == 0 {
			cfg.ActiveDeadline = int64(20 + rng.Intn(200))
		}

		p := mustPod(t, cfg)
		np := newNaive(cfg)
		total := cfg.InitContainers + cfg.AppContainers

		for step := 0; step < 60; step++ {
			op := rng.Intn(4)
			c := rng.Intn(total+2) - 1
			now := int64(rng.Intn(300))
			if rng.Intn(7) == 0 {
				now = int64(rng.Intn(int(np.maxNow) + 2))
			}
			code := int64(0)
			if op == 1 {
				if rng.Intn(2) == 0 {
					code = 1
				}
			}

			input := "iter=" + nitoa(int64(iter)) + " step=" + nitoa(int64(step)) +
				" cfg{I=" + nitoa(int64(cfg.InitContainers)) +
				" A=" + nitoa(int64(cfg.AppContainers)) +
				" pol=" + nitoa(int64(cfg.RestartPolicy)) +
				" B=" + nitoa(cfg.BackoffBase) + " M=" + nitoa(cfg.BackoffMax) +
				" R=" + nitoa(cfg.BackoffReset) + " D=" + nitoa(cfg.ActiveDeadline) +
				" X=" + nitoa(cfg.RestartBudget) + "}"

			var got, want, basis string
			switch op {
			case 0:
				input += " Start(c=" + nitoa(int64(c)) + ",now=" + nitoa(now) + ")"
				got = errName(p.Start(c, now))
				want, basis = np.start(c, now)
			case 1:
				input += " Exit(c=" + nitoa(int64(c)) + ",code=" + nitoa(code) + ",now=" + nitoa(now) + ")"
				got = errName(p.Exit(c, code, now))
				want, basis = np.exit(c, code, now)
			case 2:
				input += " Phase(now=" + nitoa(now) + ")"
				ph, err := p.Phase(now)
				if err != nil {
					got = "reject:invalid-argument"
				} else {
					got = ph.String()
				}
				want, basis = np.phase(now)
			case 3:
				input += " Reason(c=" + nitoa(int64(c)) + ",now=" + nitoa(now) + ")"
				rs, err := p.Reason(c, now)
				if err != nil {
					got = "reject:invalid-argument"
				} else {
					got = rs
				}
				want, basis = np.reason(c, now)
			}

			t.Logf("INPUT  %s", input)
			t.Logf("OUTPUT got=%s want=%s", got, want)
			t.Logf("BASIS  %s", basis)

			if got != want {
				t.Fatalf("DIFF %s: got=%s want=%s basis=%s", input, got, want, basis)
			}
			if !snapshotsEqual(t, p, np) {
				t.Fatalf("STATE DIFF %s", input)
			}
		}
	}
}
