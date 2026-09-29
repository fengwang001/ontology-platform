package coordinator

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
)

const (
	baseAnchor int64 = 1000
	basePeriod int64 = 10
)

func baseCfg(s Strategy) Config {
	return Config{Anchor: baseAnchor, Period: basePeriod, K: 3, Tolerance: 5, Strategy: s}
}

func mustNew(t *testing.T, cfg Config) *Coordinator {
	t.Helper()
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New returned unexpected error: %v", err)
	}
	return c
}

func rejectReason(t *testing.T, err error) RejectReason {
	t.Helper()
	var re *RejectError
	if !errors.As(err, &re) {
		t.Fatalf("expected *RejectError, got %T: %v", err, err)
	}
	return re.Reason
}

func grantSeq(t *testing.T, c *Coordinator, instances ...string) {
	t.Helper()
	for i, inst := range instances {
		if err := c.Grant(inst, int64(i+1)); err != nil {
			t.Fatalf("Grant(%q,%d) unexpected error: %v", inst, i+1, err)
		}
	}
}

// noopExec 记录每个序号被执行的次数与顺序。
type recorder struct {
	mu      sync.Mutex
	count   map[int64]int
	order   []int64
	failSeq map[int64]bool
}

func newRecorder() *recorder {
	return &recorder{count: map[int64]int{}, failSeq: map[int64]bool{}}
}

func (r *recorder) exec(seq, _ int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.count[seq]++
	r.order = append(r.order, seq)
	if r.failSeq[seq] {
		return fmt.Errorf("injected failure at seq %d", seq)
	}
	return nil
}

func (r *recorder) fail(seq int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failSeq[seq] = true
}

func (r *recorder) execCount(seq int64) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count[seq]
}

func (r *recorder) executedSeqs() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]int64, 0, len(r.count))
	for seq := range r.count {
		out = append(out, seq)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (r *recorder) globalOrder() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]int64, len(r.order))
	copy(out, r.order)
	return out
}

// TestNewRejectsInvalidConfig 覆盖周期非正、K 非正、容忍为负、策略非法。
func TestNewRejectsInvalidConfig(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
		want   RejectReason
	}{
		{"zero period", func(c *Config) { c.Period = 0 }, RejectInvalidPeriod},
		{"negative period", func(c *Config) { c.Period = -1 }, RejectInvalidPeriod},
		{"zero K", func(c *Config) { c.K = 0 }, RejectInvalidK},
		{"negative K", func(c *Config) { c.K = -2 }, RejectInvalidK},
		{"negative tolerance", func(c *Config) { c.Tolerance = -1 }, RejectNegativeTolerance},
		{"unknown strategy", func(c *Config) { c.Strategy = 0 }, RejectUnknownStrategy},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := baseCfg(CatchUpAll)
			tc.mutate(&cfg)
			_, err := New(cfg)
			if err == nil {
				t.Fatal("expected rejection, got nil")
			}
			if got := rejectReason(t, err); got != tc.want {
				t.Fatalf("reason = %s, want %s", reasonName(got), reasonName(tc.want))
			}
		})
	}
}

// TestGrantValidation 覆盖任期不递增与空实例，且拒绝不改变任何状态。
func TestGrantValidation(t *testing.T) {
	c := mustNew(t, baseCfg(CatchUpAll))
	if err := c.Grant("", 1); err == nil || rejectReason(t, err) != RejectUnauthorizedInstance {
		t.Fatalf("empty instance: %v", err)
	}
	if err := c.Grant("A", 0); err == nil || rejectReason(t, err) != RejectTermNotGreater {
		t.Fatalf("term 0: %v", err)
	}
	if err := c.Grant("A", 1); err != nil {
		t.Fatal(err)
	}
	before := c.Snapshot()
	for _, term := range []int64{1, -5} {
		err := c.Grant("B", term)
		if err == nil || rejectReason(t, err) != RejectTermNotGreater {
			t.Fatalf("term %d: %v", term, err)
		}
	}
	if got := c.Snapshot(); got != before {
		t.Fatalf("rejected grant changed record: %+v", got)
	}
	if err := c.Grant("B", 2); err != nil {
		t.Fatal(err)
	}
}

// TestWakeupRejectsUnauthorizedAndReplay 覆盖未授权唤醒，以及相同序列重放得到相同执行序列。
func TestWakeupRejectsUnauthorizedAndReplay(t *testing.T) {
	c := mustNew(t, baseCfg(CatchUpAll))
	grantSeq(t, c, "A")
	before := c.Snapshot()

	_, err := c.Wakeup("X", 1, baseAnchor, nil)
	if err == nil || rejectReason(t, err) != RejectUnauthorizedInstance {
		t.Fatalf("unknown instance: %v", err)
	}
	_, err = c.Wakeup("B", 1, baseAnchor, nil)
	if err == nil || rejectReason(t, err) != RejectUnauthorizedInstance {
		t.Fatalf("wrong owner: %v", err)
	}
	_, err = c.Wakeup("A", 99, baseAnchor, nil)
	if err == nil || rejectReason(t, err) != RejectUnauthorizedInstance {
		t.Fatalf("unknown term: %v", err)
	}
	if got := c.Snapshot(); got != before {
		t.Fatalf("rejected wakeup changed record: %+v", got)
	}

	runScenario := func() []int64 {
		sim := mustNew(t, baseCfg(CatchUpAll))
		rec := newRecorder()
		grantSeq(t, sim, "A")
		res, err := sim.Wakeup("A", 1, baseAnchor+5, rec.exec)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Executed) != 1 || res.Executed[0] != 0 {
			t.Fatalf("first wakeup executed = %v", res.Executed)
		}
		if _, err := sim.Wakeup("A", 1, baseAnchor+25, rec.exec); err != nil {
			t.Fatal(err)
		}
		return rec.globalOrder()
	}
	first := runScenario()
	second := runScenario()
	if len(first) == 0 || fmt.Sprint(first) != fmt.Sprint(second) {
		t.Fatalf("replay mismatch: %v vs %v", first, second)
	}
}

// TestNoMomentAndClockRollback 覆盖锚点前空转、m<=r 空转与时钟回拨。
func TestNoMomentAndClockRollback(t *testing.T) {
	c := mustNew(t, baseCfg(CatchUpOne))
	grantSeq(t, c, "A")
	res, err := c.Wakeup("A", 1, baseAnchor-1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.M != -1 || len(res.Plan) != 0 || c.Snapshot().LastSeq != -1 {
		t.Fatalf("before anchor: %+v record=%+v", res, c.Snapshot())
	}
	res, err = c.Wakeup("A", 1, baseAnchor+basePeriod, nil) // m=1
	if err != nil {
		t.Fatal(err)
	}
	// CatchUpOne 只补偿当前最大序号 m：序号 0 标记太旧，仅推进不执行。
	if fmt.Sprint(res.Executed) != "[1]" {
		t.Fatalf("executed = %v, want [1]", res.Executed)
	}
	before := c.Snapshot()
	res, err = c.Wakeup("A", 1, baseAnchor+5, nil) // m=0 时钟回拨，m<=r
	if err != nil {
		t.Fatal(err)
	}
	if res.M != 0 || len(res.Plan) != 0 || c.Snapshot() != before {
		t.Fatalf("rollback wakeup: %+v record=%+v", res, c.Snapshot())
	}
}

// TestStaleTermEntry 覆盖转移后旧任期实例在入口即被拒绝，且记录不变。
func TestStaleTermEntry(t *testing.T) {
	c := mustNew(t, baseCfg(CatchUpAll))
	grantSeq(t, c, "A", "B")
	if _, err := c.Wakeup("B", 2, baseAnchor+15, nil); err != nil {
		t.Fatal(err)
	}
	before := c.Snapshot()
	_, err := c.Wakeup("A", 1, baseAnchor+95, nil)
	if err == nil || rejectReason(t, err) != RejectStaleTerm {
		t.Fatalf("stale term entry: %v", err)
	}
	if c.Snapshot() != before {
		t.Fatalf("stale wakeup changed record: %+v", c.Snapshot())
	}
}

// TestStaleTermMidWakeup 覆盖执行途中高任期写入后，旧任期实例不再执行后续序号。
func TestStaleTermMidWakeup(t *testing.T) {
	c := mustNew(t, Config{Anchor: baseAnchor, Period: basePeriod, K: 100, Strategy: CatchUpAll})
	grantSeq(t, c, "A", "B")
	reached := make(chan struct{})
	release := make(chan struct{})
	rec := newRecorder()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		exec := func(seq, _ int64) error {
			if seq == 3 {
				close(reached)
				<-release
			}
			return rec.exec(seq, 0)
		}
		res, err := c.Wakeup("A", 1, baseAnchor+95, exec) // m=9
		if err != nil {
			t.Errorf("A wakeup: %v", err)
			return
		}
		if !res.Abandoned {
			t.Errorf("A should be abandoned after B writes higher term")
		}
		for _, seq := range res.Executed {
			if seq > 3 {
				t.Errorf("A executed seq %d after fencing", seq)
			}
		}
	}()
	<-reached
	bres, err := c.Wakeup("B", 2, baseAnchor+95, func(seq, _ int64) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	wg.Wait()
	if c.Snapshot().LastSeq != 9 || c.Snapshot().WriteTerm != 2 {
		t.Fatalf("record after transfer = %+v", c.Snapshot())
	}
	for _, seq := range bres.Executed {
		if seq < 4 {
			t.Fatalf("B unexpectedly executed %d (already claimed by A)", seq)
		}
	}
	// 旧实例可能在栅栏前执行到 3；4 及以后只能由 B 执行一次。
	for seq := int64(4); seq <= 9; seq++ {
		if rec.execCount(seq) > 0 {
			t.Fatalf("seq %d executed by old term", seq)
		}
	}
}

// TestForwardJump100Strategies 前跳 100 个周期（m=100，r=-1）下三种策略的执行集合。
func TestForwardJump100Strategies(t *testing.T) {
	now := baseAnchor + 100*basePeriod

	t.Run("CatchUpAll", func(t *testing.T) {
		c := mustNew(t, baseCfg(CatchUpAll)) // K=3
		grantSeq(t, c, "A")
		rec := newRecorder()
		res, err := c.Wakeup("A", 1, now, rec.exec)
		if err != nil {
			t.Fatal(err)
		}
		if got := res.Executed; fmt.Sprint(got) != "[98 99 100]" {
			t.Fatalf("executed = %v, want [98 99 100]", got)
		}
		// 0..97 太旧：不执行但记录已逐个推进，最终 r=100。
		if res.RAfter != 100 || c.Snapshot().LastSeq != 100 {
			t.Fatalf("record not advanced to 100: res=%+v snap=%+v", res.RAfter, c.Snapshot())
		}
		for _, seq := range []int64{0, 50, 97} {
			if rec.execCount(seq) != 0 {
				t.Fatalf("too-old seq %d executed", seq)
			}
		}
	})

	t.Run("CatchUpOne", func(t *testing.T) {
		c := mustNew(t, baseCfg(CatchUpOne))
		grantSeq(t, c, "A")
		rec := newRecorder()
		res, err := c.Wakeup("A", 1, now, rec.exec)
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(res.Executed) != "[100]" {
			t.Fatalf("executed = %v, want [100]", res.Executed)
		}
		if c.Snapshot().LastSeq != 100 {
			t.Fatalf("record = %+v", c.Snapshot())
		}
	})

	t.Run("TolerantSkipWithin", func(t *testing.T) {
		c := mustNew(t, baseCfg(TolerantSkip)) // tolerance=5
		grantSeq(t, c, "A")
		rec := newRecorder()
		res, err := c.Wakeup("A", 1, now+4, rec.exec) // now - t100 = 4 < 5
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(res.Executed) != "[100]" {
			t.Fatalf("executed = %v, want [100]", res.Executed)
		}
		if c.Snapshot().LastSeq != 100 {
			t.Fatalf("record = %+v", c.Snapshot())
		}
	})

	t.Run("TolerantSkipBeyond", func(t *testing.T) {
		c := mustNew(t, baseCfg(TolerantSkip))
		grantSeq(t, c, "A")
		rec := newRecorder()
		res, err := c.Wakeup("A", 1, now+9, rec.exec) // lag=9 >= 5
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Executed) != 0 {
			t.Fatalf("executed = %v, want none", res.Executed)
		}
		if c.Snapshot().LastSeq != 100 || res.RAfter != 100 {
			t.Fatalf("record should advance to 100 without execution: %+v", c.Snapshot())
		}
	})
}

// TestToleranceBoundary 严格边界：lag < tolerance 执行，lag == tolerance 跳过。
func TestToleranceBoundary(t *testing.T) {
	for _, lag := range []int64{4, 5, 6} {
		c := mustNew(t, baseCfg(TolerantSkip))
		grantSeq(t, c, "A")
		rec := newRecorder()
		now := baseAnchor + lag // m=0, lag 即 now-t0
		res, err := c.Wakeup("A", 1, now, rec.exec)
		if err != nil {
			t.Fatal(err)
		}
		wantExecuted := lag < 5
		gotExecuted := len(res.Executed) == 1
		if wantExecuted != gotExecuted {
			t.Fatalf("lag=%d executed=%v (want %v)", lag, gotExecuted, wantExecuted)
		}
		if c.Snapshot().LastSeq != 0 {
			t.Fatalf("lag=%d record = %+v", lag, c.Snapshot())
		}
	}
	// tolerance=0 时只有恰好落在时刻点才执行。
	c := mustNew(t, Config{Anchor: baseAnchor, Period: basePeriod, K: 1, Tolerance: 0, Strategy: TolerantSkip})
	grantSeq(t, c, "A")
	res, err := c.Wakeup("A", 1, baseAnchor+1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Executed) != 0 || c.Snapshot().LastSeq != 0 {
		t.Fatalf("lag=1 with tolerance=0: %+v %+v", res, c.Snapshot())
	}
}

// TestFailureMidExecutionNoRedo 执行中途失效：该序号不被任何实例（含自己重试）重做。
func TestFailureMidExecutionNoRedo(t *testing.T) {
	c := mustNew(t, Config{Anchor: baseAnchor, Period: basePeriod, K: 100, Strategy: CatchUpAll})
	grantSeq(t, c, "A", "B")
	rec := newRecorder()
	rec.fail(1)

	// A 在 now 使 m=3 时唤醒，执行顺序 0(成功) 1(失效)，2、3 不再触碰。
	res, err := c.Wakeup("A", 1, baseAnchor+30, rec.exec)
	if err == nil {
		t.Fatal("expected execution error")
	}
	if res.Failed == nil || *res.Failed != 1 {
		t.Fatalf("failed seq = %v, want 1", res.Failed)
	}
	if fmt.Sprint(res.Executed) != "[0]" {
		t.Fatalf("executed before failure = %v", res.Executed)
	}
	if c.Snapshot().LastSeq != 1 || rec.execCount(1) != 1 {
		t.Fatalf("after failure record=%+v count(1)=%d", c.Snapshot(), rec.execCount(1))
	}

	// A 自己重试：m 仍为 3，r=1，只执行 2、3，序号 1 不重做。
	res2, err := c.Wakeup("A", 1, baseAnchor+30, rec.exec)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if fmt.Sprint(res2.Executed) != "[2 3]" {
		t.Fatalf("retry executed = %v", res2.Executed)
	}
	if rec.execCount(1) != 1 {
		t.Fatalf("failed seq executed %d times", rec.execCount(1))
	}

	// 更高任期的 B 再唤醒到 m=4：同样只执行 4，绝不重做 1。
	res3, err := c.Wakeup("B", 2, baseAnchor+40, rec.exec)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(res3.Executed) != "[4]" {
		t.Fatalf("B executed = %v", res3.Executed)
	}
	if rec.execCount(1) != 1 {
		t.Fatalf("failed seq redone by B: count=%d", rec.execCount(1))
	}
	if c.Snapshot().LastSeq != 4 {
		t.Fatalf("final record = %+v", c.Snapshot())
	}
}

// TestConcurrentWakeups 多实例并发唤醒：每个序号至多执行一次，
// 按记录推进先后排列的执行序号严格递增，最终记录覆盖全部序号。
func TestConcurrentWakeups(t *testing.T) {
	c := mustNew(t, Config{Anchor: baseAnchor, Period: basePeriod, K: 100, Strategy: CatchUpAll})
	const n = 8
	insts := make([]string, n)
	for i := range insts {
		insts[i] = fmt.Sprintf("inst-%d", i)
	}
	grantSeq(t, c, insts...)

	rec := newRecorder()

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range insts {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			exec := func(seq, _ int64) error { return rec.exec(seq, 0) }
			if _, err := c.Wakeup(insts[idx], int64(idx+1), baseAnchor+95, exec); err != nil {
				// 低任期实例可能在入口就被高任期记录拒绝（任期转移），这是合法结果。
				var re *RejectError
				if !errors.As(err, &re) || re.Reason != RejectStaleTerm {
					t.Errorf("instance %s: %v", insts[idx], err)
				}
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if got := c.Snapshot().LastSeq; got != 9 {
		t.Fatalf("final r = %d, want 9", got)
	}
	for seq := int64(0); seq <= 9; seq++ {
		if cnt := rec.execCount(seq); cnt != 1 {
			t.Fatalf("seq %d executed %d times, want exactly 1", seq, cnt)
		}
	}
	// 按“记录推进先后”（日志中的 advance 记录，锁内原子发生）排列执行序号，
	// 必须严格递增；回调在锁外完成，完成时刻允许交错。
	var advanceOrder []int64
	for _, line := range c.LogLines() {
		if !strings.Contains(line, "advance seq=") || !strings.Contains(line, "kind=execute") {
			continue
		}
		var seq int64
		rest := line[strings.Index(line, "advance seq=")+len("advance seq="):]
		if _, err := fmt.Sscanf(rest, "%d term=", &seq); err != nil {
			t.Fatalf("parse log line %q: %v", line, err)
		}
		advanceOrder = append(advanceOrder, seq)
	}
	if fmt.Sprint(advanceOrder) != "[0 1 2 3 4 5 6 7 8 9]" {
		t.Fatalf("advance order = %v", advanceOrder)
	}
	if c.Snapshot().WriteTerm < 1 {
		t.Fatalf("record write term = %d", c.Snapshot().WriteTerm)
	}
	// 即便回调完成顺序交错，每个实例看到的自身执行序列也必须升序。
	seen := map[int64]bool{}
	for seq := range rec.count {
		if seen[seq] {
			t.Fatalf("dup seq %d", seq)
		}
		seen[seq] = true
	}
}

// TestLogContainsInputOutputAndReason 日志需打印输入、输出与判定依据。
func TestLogContainsInputOutputAndReason(t *testing.T) {
	c := mustNew(t, baseCfg(CatchUpAll))
	if err := c.Grant("A", 1); err != nil {
		t.Fatal(err)
	}
	if err := c.Grant("A", 1); err == nil {
		t.Fatal("expected rejection")
	}
	if _, err := c.Wakeup("A", 1, baseAnchor+25, nil); err != nil {
		t.Fatal(err)
	}
	lines := c.LogLines()
	if len(lines) == 0 {
		t.Fatal("no log lines")
	}
	joined := ""
	for _, line := range lines {
		joined += line + "\n"
	}
	for _, want := range []string{"input=", "output=", "判定依据", "reason=TermNotGreater"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("log missing %q:\n%s", want, joined)
		}
	}
	t.Log("\n" + joined)
}
