package scheduler

import (
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

var anchor = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

const period = time.Second

type recorder struct {
	mu     sync.Mutex
	seqs   []int
	failAt int
}

// 执行中途失效：失效序号已被认领推进，任何实例都不会重做；其余实例继续补后续序号。
func TestMidExecutionFailureNoRedo(t *testing.T) {
	rc := newRecorder()
	rc.failAt = 2
	c := newCoordinator(t, CatchUpAll, 5, 0, rc)
	mustGrant(t, c, 1, "A")
	mustGrant(t, c, 2, "B")

	// A 补偿 0..3（K=5 足够大），在序号 2"崩溃"：0,1,2 已推进记录，3 未认领。
	res, err := c.Wake("A", anchor.Add(3*period))
	if err != nil {
		t.Fatal(err)
	}
	if !equal(res.Executed, []int{0, 1, 2}) || res.AbortedAt != 2 {
		t.Fatalf("unexpected abort result: %+v", res)
	}
	if r, term := c.LastExecuted(); r != 2 || term != 1 {
		t.Fatalf("record after abort = (%d,%d), want (2,1)", r, term)
	}

	// A 自己重试：2 已在记录中，只执行 3；2 绝不重做。
	res, _ = c.Wake("A", anchor.Add(3*period))
	if !equal(res.Executed, []int{3}) {
		t.Fatalf("A retry executed=%v, want [3]", res.Executed)
	}

	// B 以更高任期接手 4..6；同样不会碰 2。
	res, _ = c.Wake("B", anchor.Add(6*period))
	if !equal(res.Executed, []int{4, 5, 6}) {
		t.Fatalf("B executed=%v, want [4 5 6]", res.Executed)
	}

	got := rc.snapshot()
	want := []int{0, 1, 2, 3, 4, 5, 6}
	if !equal(got, want) {
		t.Fatalf("global executions=%v, want %v (seq 2 executed exactly once)", got, want)
	}
	if n := count(got, 2); n != 1 {
		t.Fatalf("seq 2 executed %d times, want exactly 1", n)
	}
}

func count(xs []int, v int) int {
	n := 0
	for _, x := range xs {
		if x == v {
			n++
		}
	}
	return n
}

// 并发唤醒（同任期单实例重入 + 多实例多任期交错）：
// 每个序号全局至多执行一次；按记录推进先后，执行序号严格递增。
func TestConcurrentWakes(t *testing.T) {
	rc := newRecorder()
	// 串行化 executor 以观察全局执行顺序。
	var execMu sync.Mutex
	var order []int
	base := rc.exec
	cfg := Config{
		Anchor: anchor, Period: period, Policy: CatchUpAll, K: 1000,
		LogOutput: io.Discard,
		Executor: func(seq int) error {
			execMu.Lock()
			order = append(order, seq)
			execMu.Unlock()
			return base(seq)
		},
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	mustGrant(t, c, 1, "A")
	mustGrant(t, c, 2, "B")
	mustGrant(t, c, 3, "C")

	const goroutines = 24
	var wg sync.WaitGroup
	wg.Add(goroutines)
	instances := []string{"A", "B", "C"}
	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			now := anchor.Add(time.Duration(50+i%7) * period)
			_, _ = c.Wake(instances[i%len(instances)], now)
		}()
	}
	wg.Wait()

	execMu.Lock()
	got := append([]int(nil), order...)
	execMu.Unlock()

	if len(got) == 0 {
		t.Fatal("nothing executed")
	}
	seen := map[int]bool{}
	for _, s := range got {
		if seen[s] {
			t.Fatalf("seq %d executed more than once; full order=%v", s, got)
		}
		seen[s] = true
	}
	for i := 1; i < len(got); i++ {
		if got[i] <= got[i-1] {
			t.Fatalf("execution order not strictly increasing by record advance: %v", got)
		}
	}
	if r, _ := c.LastExecuted(); r < 50 {
		t.Fatalf("final r=%d, expected all wakes up to m=56 covered", r)
	}
}

// 相同的授权、时钟与唤醒序列重放，得到相同的执行序列。
func TestDeterministicReplay(t *testing.T) {
	script := []struct {
		inst string
		at   time.Duration
	}{
		{"A", 0}, {"A", 3 * time.Second}, {"B", 10 * time.Second},
		{"A", 7 * time.Second}, {"B", 100 * time.Second},
	}
	run := func() []int {
		rc := newRecorder()
		c := newCoordinator(t, CatchUpAll, 4, 0, rc)
		mustGrant(t, c, 1, "A")
		mustGrant(t, c, 2, "B")
		for _, step := range script {
			if _, err := c.Wake(step.inst, anchor.Add(step.at)); err != nil {
				t.Fatal(err)
			}
		}
		return rc.snapshot()
	}
	first := run()
	for i := 0; i < 5; i++ {
		if !equal(run(), first) {
			t.Fatalf("replay %d diverged: %v vs %v", i, run(), first)
		}
	}
}

// 并发场景下任期转移抢占：旧任期在认领阶段发现记录已由高任期写入即停止。
func TestConcurrentTermTransferPreemption(t *testing.T) {
	rc := newRecorder()
	started := make(chan int, 1000)
	release := make(chan struct{})
	cfg := Config{
		Anchor: anchor, Period: period, Policy: CatchUpAll, K: 1000,
		LogOutput: io.Discard,
		Executor: func(seq int) error {
			started <- seq
			<-release
			return rc.exec(seq)
		},
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	mustGrant(t, c, 1, "A")
	mustGrant(t, c, 2, "B")

	awake := make(chan struct{})
	go func() {
		_, _ = c.Wake("A", anchor.Add(20*period))
		close(awake)
	}()
	first := <-started // A 已认领并开始执行序号 0，记录此刻 r=0/term=1

	done := make(chan struct{})
	go func() {
		_, _ = c.Wake("B", anchor.Add(20*period))
		close(done)
	}()
	// 给 B 一点时间进入认领循环（它会阻塞在执行或停在序号 1）。
	// 释放 A 的序号 0 后，让 B 跑完：B 只能执行 A 未认领的 1..20。
	close(release)
	if first != 0 {
		t.Fatalf("first claimed seq = %d, want 0", first)
	}
	<-awake
	<-done

	got := rc.snapshot()
	seen := map[int]bool{}
	for _, s := range got {
		if seen[s] {
			t.Fatalf("seq %d duplicated; order=%v", s, got)
		}
		seen[s] = true
	}
	if len(got) != 21 {
		t.Fatalf("executed %d seqs %v, want exactly 0..20 once each", len(got), got)
	}
}

func newRecorder() *recorder { return &recorder{failAt: -1} }

func (rc *recorder) exec(seq int) error {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.seqs = append(rc.seqs, seq)
	if seq == rc.failAt {
		return errors.New("instance crashed mid-execution")
	}
	return nil
}

func (rc *recorder) snapshot() []int {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	out := make([]int, len(rc.seqs))
	copy(out, rc.seqs)
	return out
}

func newCoordinator(t *testing.T, policy Policy, k int, tol time.Duration, rc *recorder) *Coordinator {
	t.Helper()
	c, err := New(Config{
		Anchor:    anchor,
		Period:    period,
		Policy:    policy,
		K:         k,
		Tolerance: tol,
		Executor:  rc.exec,
		LogOutput: &testLogWriter{t: t},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

type testLogWriter struct{ t *testing.T }

func (w *testLogWriter) Write(p []byte) (int, error) {
	w.t.Logf("[scheduler] %s", p)
	return len(p), nil
}

func rejectReason(t *testing.T, err error) RejectReason {
	t.Helper()
	var re *RejectError
	if !errors.As(err, &re) {
		t.Fatalf("expected *RejectError, got %v", err)
	}
	return re.Reason
}

func mustGrant(t *testing.T, c *Coordinator, term int, instance string) {
	t.Helper()
	if err := c.Grant(term, instance); err != nil {
		t.Fatalf("grant term=%d instance=%s: %v", term, instance, err)
	}
}

func equal(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// 构造参数非法时整体拒绝，且原因可区分。
func TestNewValidation(t *testing.T) {
	cases := []struct {
		name   string
		cfg    Config
		reason RejectReason
	}{
		{"zero period", Config{Period: 0, Policy: CatchUpAll, K: 3}, ReasonInvalidPeriod},
		{"negative period", Config{Period: -time.Second, Policy: CatchUpAll, K: 3}, ReasonInvalidPeriod},
		{"non-positive K", Config{Period: period, Policy: CatchUpAll, K: 0}, ReasonInvalidK},
		{"negative K", Config{Period: period, Policy: CatchUpAll, K: -2}, ReasonInvalidK},
		{"negative tolerance", Config{Period: period, Policy: TolerantSkip, Tolerance: -time.Nanosecond}, ReasonInvalidTolerance},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg
			cfg.Anchor = anchor
			cfg.LogOutput = io.Discard
			if _, err := New(cfg); err == nil {
				t.Fatal("expected rejection")
			} else if got := rejectReason(t, err); got != tc.reason {
				t.Fatalf("reason = %s, want %s", got, tc.reason)
			}
		})
	}
}

// K 与 tolerance 只约束相关策略。
func TestNewOtherPoliciesDoNotRequireK(t *testing.T) {
	for _, p := range []Policy{CatchUpOne, TolerantSkip} {
		if _, err := New(Config{Anchor: anchor, Period: period, Policy: p, LogOutput: io.Discard}); err != nil {
			t.Fatalf("policy %d: unexpected error %v", p, err)
		}
	}
}

// 授权任期必须严格递增；唤醒未授权实例被拒；拒绝不改变记录。
func TestGrantAndUnauthorizedWake(t *testing.T) {
	c := newCoordinator(t, CatchUpOne, 1, 0, newRecorder())
	mustGrant(t, c, 1, "A")
	if err := c.Grant(1, "B"); err == nil || rejectReason(t, err) != ReasonStaleTerm {
		t.Fatalf("equal term must be rejected as %s, got %v", ReasonStaleTerm, err)
	}
	if err := c.Grant(0, "C"); err == nil || rejectReason(t, err) != ReasonStaleTerm {
		t.Fatalf("smaller term must be rejected as %s, got %v", ReasonStaleTerm, err)
	}
	if _, err := c.Wake("Z", anchor.Add(5*time.Second)); err == nil ||
		rejectReason(t, err) != ReasonUnauthorized {
		t.Fatalf("unauthorized wake must be rejected as %s, got %v", ReasonUnauthorized, err)
	}
	if r, term := c.LastExecuted(); r != -1 || term != 0 {
		t.Fatalf("record changed by rejected ops: r=%d term=%d", r, term)
	}
}

// m 不存在或 m<=r 时什么也不做；时钟回拨同样不动作、不执行。
func TestWakeNoopAndClockRewind(t *testing.T) {
	rc := newRecorder()
	c := newCoordinator(t, CatchUpOne, 1, 0, rc)
	mustGrant(t, c, 1, "A")

	res, err := c.Wake("A", anchor.Add(-time.Second))
	if err != nil || res.M != -1 || len(rc.snapshot()) != 0 {
		t.Fatalf("before anchor: res=%+v err=%v exec=%v", res, err, rc.snapshot())
	}
	res, _ = c.Wake("A", anchor)
	if res.M != 0 || !equal(res.Executed, []int{0}) {
		t.Fatalf("at anchor: %+v", res)
	}
	res, _ = c.Wake("A", anchor.Add(500*time.Millisecond))
	if res.M != 0 || len(res.Executed) != 0 {
		t.Fatalf("rewind must noop: %+v", res)
	}
	res, _ = c.Wake("A", anchor.Add(-3*time.Second))
	if res.M != -1 || len(res.Executed) != 0 {
		t.Fatalf("rewind before anchor must noop: %+v", res)
	}
	if got := rc.snapshot(); !equal(got, []int{0}) {
		t.Fatalf("global executions = %v, want [0]", got)
	}
}

// 任期转移后，记录被更高任期写入时旧任期实例放弃；旧任期先写入则可运行。
func TestOldTermInstanceYieldsAfterTransfer(t *testing.T) {
	t.Run("yields after higher-term write", func(t *testing.T) {
		rc := newRecorder()
		c := newCoordinator(t, CatchUpOne, 1, 0, rc)
		mustGrant(t, c, 1, "A")
		mustGrant(t, c, 2, "B")

		if _, err := c.Wake("B", anchor); err != nil {
			t.Fatal(err)
		}
		res, err := c.Wake("A", anchor.Add(3*time.Second))
		if err != nil {
			t.Fatalf("yielding is not a rejection: %v", err)
		}
		if len(res.Executed) != 0 {
			t.Fatalf("old-term instance must not execute, got %v", res.Executed)
		}
		res, _ = c.Wake("B", anchor.Add(3*time.Second))
		if !equal(res.Executed, []int{3}) {
			t.Fatalf("new term must execute m=3, got %+v", res)
		}
		if got := rc.snapshot(); !equal(got, []int{0, 3}) {
			t.Fatalf("executions = %v, want [0 3]", got)
		}
	})

	t.Run("old term may run before higher-term write", func(t *testing.T) {
		rc := newRecorder()
		c := newCoordinator(t, CatchUpOne, 1, 0, rc)
		mustGrant(t, c, 1, "A")
		mustGrant(t, c, 2, "B")

		// 记录任期初始为 0，任期 1 不小于它：A 可以执行。
		res, err := c.Wake("A", anchor.Add(time.Second))
		if err != nil || !equal(res.Executed, []int{1}) {
			t.Fatalf("term 1 should run before any term-2 write: %+v err=%v", res, err)
		}
		if r, term := c.LastExecuted(); r != 1 || term != 1 {
			t.Fatalf("record = (%d,%d), want (1,1)", r, term)
		}
	})
}

// 前跳 100 个周期：三种策略的执行集合各不相同。
func TestForwardJump100Periods(t *testing.T) {
	t.Run("catch_up_all_K3", func(t *testing.T) {
		rc := newRecorder()
		c := newCoordinator(t, CatchUpAll, 3, 0, rc)
		mustGrant(t, c, 1, "A")
		if _, err := c.Wake("A", anchor); err != nil {
			t.Fatal(err)
		}
		res, err := c.Wake("A", anchor.Add(100*period))
		if err != nil {
			t.Fatal(err)
		}
		if !equal(res.Executed, []int{98, 99, 100}) {
			t.Fatalf("executed=%v want [98 99 100]; skipped=%v", res.Executed, res.Skipped)
		}
		if len(res.Skipped) != 97 || res.Skipped[0] != 1 || res.Skipped[96] != 97 {
			t.Fatalf("skipped=%v, want 1..97", res.Skipped)
		}
		if r, _ := c.LastExecuted(); r != 100 {
			t.Fatalf("record r=%d, want 100", r)
		}
		if got := rc.snapshot(); !equal(got, []int{0, 98, 99, 100}) {
			t.Fatalf("global executions=%v", got)
		}
	})

	t.Run("catch_up_all_K_larger_than_gap", func(t *testing.T) {
		rc := newRecorder()
		c := newCoordinator(t, CatchUpAll, 1000, 0, rc)
		mustGrant(t, c, 1, "A")
		res, _ := c.Wake("A", anchor.Add(100*period))
		if len(res.Executed) != 101 || res.Executed[0] != 0 || res.Executed[100] != 100 {
			t.Fatalf("want 0..100 ascending, got %d entries head=%v tail=%d",
				len(res.Executed), res.Executed[:3], res.Executed[len(res.Executed)-1])
		}
	})

	t.Run("catch_up_one", func(t *testing.T) {
		rc := newRecorder()
		c := newCoordinator(t, CatchUpOne, 1, 0, rc)
		mustGrant(t, c, 1, "A")
		if _, err := c.Wake("A", anchor); err != nil {
			t.Fatal(err)
		}
		res, _ := c.Wake("A", anchor.Add(100*period))
		if !equal(res.Executed, []int{100}) {
			t.Fatalf("executed=%v want [100]", res.Executed)
		}
		if len(res.Skipped) != 99 || res.Skipped[0] != 1 || res.Skipped[98] != 99 {
			t.Fatalf("skipped=%v, want 1..99", res.Skipped)
		}
		if r, _ := c.LastExecuted(); r != 100 {
			t.Fatalf("r=%d", r)
		}
	})

	t.Run("tolerant_skip_inside_then_outside", func(t *testing.T) {
		rc := newRecorder()
		c := newCoordinator(t, TolerantSkip, 1, 10*time.Second, rc)
		mustGrant(t, c, 1, "A")
		if _, err := c.Wake("A", anchor); err != nil {
			t.Fatal(err)
		}
		// 前跳 100 个周期恰好落在 t(100)：延迟 0<10s，执行 m=100。
		res, _ := c.Wake("A", anchor.Add(100*period))
		if !equal(res.Executed, []int{100}) {
			t.Fatalf("inside tolerance: executed=%v", res.Executed)
		}
	})

	t.Run("tolerant_skip_zero_tolerance_skips", func(t *testing.T) {
		rc := newRecorder()
		c := newCoordinator(t, TolerantSkip, 1, 0, rc)
		mustGrant(t, c, 1, "A")
		if _, err := c.Wake("A", anchor); err != nil {
			t.Fatal(err)
		}
		// 容忍 0：恰好落在触发点延迟为 0，0<0 不成立 → 跳过，记录仍推进。
		res, _ := c.Wake("A", anchor.Add(100*period))
		if len(res.Executed) != 0 {
			t.Fatalf("outside tolerance must skip execution, got %v", res.Executed)
		}
		if r, _ := c.LastExecuted(); r != 100 {
			t.Fatalf("record must advance to m=100 on skip, got %d", r)
		}
	})
}

// 触发时刻与容忍边界：latency < tolerance 执行；==tolerance 跳过。
func TestToleranceBoundary(t *testing.T) {
	tol := time.Second
	bigPeriod := 10 * time.Second
	for _, tc := range []struct {
		name    string
		offset  time.Duration
		execute bool
	}{
		{"exactly at t(m)", 0, true},
		{"one nanosecond inside", time.Second - time.Nanosecond, true},
		{"exactly at tolerance", time.Second, false},
		{"well beyond tolerance", 3 * time.Second, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rc := newRecorder()
			cfg := Config{
				Anchor: anchor, Period: bigPeriod, Policy: TolerantSkip,
				Tolerance: tol, Executor: rc.exec, LogOutput: &testLogWriter{t: t},
			}
			c, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			mustGrant(t, c, 1, "A")
			res, err := c.Wake("A", anchor.Add(5*bigPeriod+tc.offset))
			if err != nil {
				t.Fatal(err)
			}
			if tc.execute {
				if !equal(res.Executed, []int{5}) {
					t.Fatalf("executed=%v want [5]", res.Executed)
				}
			} else {
				if len(res.Executed) != 0 {
					t.Fatalf("executed=%v want none", res.Executed)
				}
				if r, _ := c.LastExecuted(); r != 5 {
					t.Fatalf("record should advance to 5, got %d", r)
				}
			}
		})
	}
}
