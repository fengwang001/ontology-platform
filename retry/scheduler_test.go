package retry

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func mustNew(t *testing.T, cfg Config, j JitterFunc) *Scheduler {
	t.Helper()
	s, err := New(cfg, j)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func mustSubmit(t *testing.T, s *Scheduler, id string, chain []string, created, ttl int64) {
	t.Helper()
	if err := s.Submit(id, chain, created, ttl); err != nil {
		t.Fatalf("Submit(%s): %v", id, err)
	}
}

func failOK(t *testing.T, s *Scheduler, id string, now int64, class Class, ra int64) Result {
	t.Helper()
	r, err := s.Fail(id, now, class, ra)
	if err != nil {
		t.Fatalf("Fail(%s, now=%d, %s, ra=%d): %v", id, now, class, ra, err)
	}
	return r
}

func wantResult(t *testing.T, got Result, ch string, nextAt int64) {
	t.Helper()
	if got.Dead || got.Channel != ch || got.NextAt != nextAt {
		t.Fatalf("got %+v, want channel=%s nextAt=%d", got, ch, nextAt)
	}
}

func wantDead(t *testing.T, got Result, reason DeadReason) {
	t.Helper()
	if !got.Dead || got.Reason != reason {
		t.Fatalf("got %+v, want dead reason=%s", got, reason)
	}
}

func wantErr(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("got err=%v, want %v", err, want)
	}
}

func constJitter(v int64) JitterFunc {
	return func(string, int64, int64) int64 { return v }
}

// 规格主示例：退避、Retry-After、切换与 budget 死信的完整回放。
func TestSpecExample(t *testing.T) {
	cfg := Config{Base: 10, Cap: 40, M: 3, A: 5, RAcap: 60, K: 1000, Cool: 100}
	s := mustNew(t, cfg, constJitter(3))
	mustSubmit(t, s, "t1", []string{"push", "sms"}, 0, 1000)

	steps := []struct {
		now   int64
		class Class
		ra    int64
		ch    string
		next  int64
		basis string
	}{
		{0, Transient, 0, "push", 8, "n=1, d=10, jitter 3 夹到 floor(10/4)=2, w=8"},
		{8, Transient, 0, "push", 25, "n=2, d=20, jitter 3, w=17"},
		{25, Throttled, 30, "sms", 25, "n=3 达到 M=3, 切换到 sms, w=0"},
		{25, Throttled, 50, "sms", 75, "n=1, d=10, jitter 夹到 2, d'=8, w=max(8,50)=50"},
	}
	for _, st := range steps {
		r := failOK(t, s, "t1", st.now, st.class, st.ra)
		t.Logf("Fail(now=%d, %s, ra=%d) -> %+v; 依据: %s", st.now, st.class, st.ra, r, st.basis)
		wantResult(t, r, st.ch, st.next)
	}
	r := failOK(t, s, "t1", 75, Transient, 0)
	t.Logf("Fail(now=75, transient) -> %+v; 依据: att=5 达到预算 A=5", r)
	wantDead(t, r, ReasonBudget)
}

// 主示例变体：ra=100 超过 RAcap=60，切换无后续渠道，死信 throttled。
func TestSpecExampleThrottledVariant(t *testing.T) {
	cfg := Config{Base: 10, Cap: 40, M: 3, A: 5, RAcap: 60, K: 1000, Cool: 100}
	s := mustNew(t, cfg, constJitter(3))
	mustSubmit(t, s, "t1", []string{"push", "sms"}, 0, 1000)
	failOK(t, s, "t1", 0, Transient, 0)
	failOK(t, s, "t1", 8, Transient, 0)
	failOK(t, s, "t1", 25, Throttled, 30)
	r := failOK(t, s, "t1", 25, Throttled, 100)
	t.Logf("ra=100 > RAcap=60 -> %+v; 依据: 切换分支无后续渠道", r)
	wantDead(t, r, ReasonThrottled)
}

// 规格熔断示例：跨任务熔断、跳过熔断渠道、openUntil == now 不算熔断。
func TestSpecCircuitExample(t *testing.T) {
	cfg := Config{Base: 10, Cap: 1000, M: 5, A: 100, RAcap: 60, K: 2, Cool: 100}
	s := mustNew(t, cfg, constJitter(0))
	mustSubmit(t, s, "a", []string{"sms"}, 0, 100000)
	failOK(t, s, "a", 10, Transient, 0) // gfail[sms]=1
	failOK(t, s, "a", 20, Transient, 0) // gfail[sms]=2 >= K=2 -> openUntil[sms]=120
	if got := s.openUntil["sms"]; got != 120 {
		t.Fatalf("openUntil[sms]=%d, want 120", got)
	}

	mustSubmit(t, s, "b", []string{"push", "sms", "email"}, 30, 100000)
	r := failOK(t, s, "b", 30, Permanent, 0)
	t.Logf("permanent at 30 -> %+v; 依据: sms 熔断至 120, 跳过选 email", r)
	wantResult(t, r, "email", 30)

	mustSubmit(t, s, "c", []string{"push", "sms", "email"}, 120, 100000)
	r = failOK(t, s, "c", 120, Permanent, 0)
	t.Logf("permanent at 120 -> %+v; 依据: openUntil==now 不算熔断, 选 sms", r)
	wantResult(t, r, "sms", 120)
}

// d 恰等于 Cap 时不再增长，超过 Cap 也被夹到 Cap。
func TestBackoffCap(t *testing.T) {
	cfg := Config{Base: 10, Cap: 40, M: 10, A: 100, RAcap: 0, K: 1000, Cool: 1}
	s := mustNew(t, cfg, constJitter(0))
	mustSubmit(t, s, "t", []string{"push"}, 0, 100000)
	// n=1: d=10; n=2: d=20; n=3: d=40==Cap; n=4: d=80 封顶 40。
	now := int64(0)
	for _, w := range []int64{10, 20, 40, 40} {
		r := failOK(t, s, "t", now, Transient, 0)
		t.Logf("Fail(now=%d, transient) -> %+v; 依据: d=min(Cap, Base*2^(n-1)), w=%d", now, r, w)
		wantResult(t, r, "push", now+w)
		now += w
	}
}

// 抖动夹取：负数夹到 0，超过 floor(d/4) 夹到 floor(d/4)，界内不夹。
func TestJitterClamp(t *testing.T) {
	jitters := []int64{-5, 99, 7}
	idx := 0
	j := func(string, int64, int64) int64 { v := jitters[idx]; idx++; return v }
	cfg := Config{Base: 40, Cap: 40, M: 10, A: 100, RAcap: 0, K: 1000, Cool: 1} // Cap 钉住 d=40
	s := mustNew(t, cfg, j)
	mustSubmit(t, s, "t", []string{"push"}, 0, 100000)
	// d 恒为 40，floor(d/4)=10。
	wants := []int64{40, 30, 33} // 夹到 0 / 夹到 10 / 不夹
	now := int64(0)
	for i, w := range wants {
		r := failOK(t, s, "t", now, Transient, 0)
		t.Logf("jitter=%d -> %+v; 依据: d'=40-clamp(%d,0,10)=%d", jitters[i], r, jitters[i], w)
		wantResult(t, r, "push", now+w)
		now += w
	}
}

// w 取 d' 与 ra 的较大者。
func TestWaitMaxOfBackoffAndRA(t *testing.T) {
	cfg := Config{Base: 10, Cap: 1000, M: 10, A: 100, RAcap: 1000, K: 1000, Cool: 1}
	s := mustNew(t, cfg, constJitter(2)) // d=10, d'=8
	mustSubmit(t, s, "t", []string{"push"}, 0, 100000)
	r := failOK(t, s, "t", 0, Throttled, 3) // ra < d' -> w=8
	t.Logf("ra=3 < d'=8 -> %+v", r)
	wantResult(t, r, "push", 8)
	r = failOK(t, s, "t", 8, Throttled, 50) // ra > d'(=18) -> w=50
	t.Logf("ra=50 > d'=18 -> %+v", r)
	wantResult(t, r, "push", 58)
}

// ra 恰等于 RAcap 时等待，大 1 时切换。
func TestRAcapBoundary(t *testing.T) {
	cfg := Config{Base: 10, Cap: 1000, M: 10, A: 100, RAcap: 60, K: 1000, Cool: 1}
	s := mustNew(t, cfg, constJitter(0))
	mustSubmit(t, s, "eq", []string{"push", "sms"}, 0, 100000)
	r := failOK(t, s, "eq", 0, Throttled, 60) // ra == RAcap -> 等待
	t.Logf("ra=60 == RAcap -> %+v; 依据: 不进入切换分支", r)
	wantResult(t, r, "push", 60)

	mustSubmit(t, s, "over", []string{"push", "sms"}, 60, 100000)
	r = failOK(t, s, "over", 60, Throttled, 61) // ra == RAcap+1 -> 切换
	t.Logf("ra=61 > RAcap -> %+v; 依据: 切换分支, w=0", r)
	wantResult(t, r, "sms", 60)
}

// n 恰等于 M 时立即切换而不再等待；切换后 n 清零、att 保留。
func TestExhaustedSwitchAndCounters(t *testing.T) {
	cfg := Config{Base: 10, Cap: 1000, M: 2, A: 100, RAcap: 0, K: 1000, Cool: 1}
	s := mustNew(t, cfg, constJitter(0))
	mustSubmit(t, s, "t", []string{"push", "sms"}, 0, 100000)
	r := failOK(t, s, "t", 0, Transient, 0) // n=1 < M -> 等待
	wantResult(t, r, "push", 10)
	r = failOK(t, s, "t", 10, Transient, 0) // n=2 == M -> 立即切换, w=0
	t.Logf("n==M=2 -> %+v; 依据: 切换分支不再计算退避", r)
	wantResult(t, r, "sms", 10)
	tk := s.tasks["t"]
	if tk.n != 0 || tk.att != 2 || tk.cur != 1 {
		t.Fatalf("after switch: n=%d att=%d cur=%d, want 0/2/1", tk.n, tk.att, tk.cur)
	}
	r = failOK(t, s, "t", 10, Transient, 0) // n 已清零 -> d=Base=10
	t.Logf("切换后首次失败 -> %+v; 依据: n=1, d=Base", r)
	wantResult(t, r, "sms", 20)
	if tk.att != 3 {
		t.Fatalf("att=%d, want 3 (切换不清零)", tk.att)
	}
}

// permanent 在中间渠道切换，在最后渠道死信 permanent。
func TestPermanentMidAndLast(t *testing.T) {
	cfg := Config{Base: 10, Cap: 1000, M: 5, A: 100, RAcap: 0, K: 1000, Cool: 1}
	s := mustNew(t, cfg, constJitter(0))
	mustSubmit(t, s, "mid", []string{"push", "sms"}, 0, 100000)
	r := failOK(t, s, "mid", 0, Permanent, 0)
	t.Logf("permanent 于中间渠道 -> %+v", r)
	wantResult(t, r, "sms", 0)

	mustSubmit(t, s, "last", []string{"sms"}, 0, 100000)
	r = failOK(t, s, "last", 0, Permanent, 0)
	t.Logf("permanent 于最后渠道 -> %+v", r)
	wantDead(t, r, ReasonPermanent)
}

// 原因优先级：切换原因先于 budget；budget 先于 expired。
func TestReasonPriority(t *testing.T) {
	// A=1：permanent 使 att=1 达到预算，但切换原因（无后续渠道）优先。
	cfg := Config{Base: 10, Cap: 1000, M: 1, A: 1, RAcap: 0, K: 1000, Cool: 1}
	s := mustNew(t, cfg, constJitter(0))
	mustSubmit(t, s, "t", []string{"push"}, 0, 100000)
	r := failOK(t, s, "t", 0, Permanent, 0)
	t.Logf("att=1>=A 且 permanent -> %+v; 依据: 切换原因先于 budget", r)
	wantDead(t, r, ReasonPermanent)

	// budget 先于 expired：att 达到 A 且 nextAt 将超过 deadline，报 budget。
	cfg = Config{Base: 1000, Cap: 1000, M: 5, A: 1, RAcap: 0, K: 1000, Cool: 1}
	s = mustNew(t, cfg, constJitter(0))
	mustSubmit(t, s, "t", []string{"push"}, 0, 10) // deadline=10
	r = failOK(t, s, "t", 0, Transient, 0)         // att=1>=A, w=1000 会超期
	t.Logf("att>=A 且 nextAt 超期 -> %+v; 依据: budget 先于 expired", r)
	wantDead(t, r, ReasonBudget)
}

// nextAt 恰等于 deadline 允许；大 1 死信 expired。
func TestDeadlineBoundary(t *testing.T) {
	cfg := Config{Base: 10, Cap: 100, M: 10, A: 100, RAcap: 0, K: 1000, Cool: 1}
	s := mustNew(t, cfg, constJitter(0))
	mustSubmit(t, s, "eq", []string{"push"}, 0, 70) // deadline=70
	failOK(t, s, "eq", 0, Transient, 0)             // nextAt=10
	failOK(t, s, "eq", 10, Transient, 0)            // nextAt=30
	r := failOK(t, s, "eq", 30, Transient, 0)       // w=40, nextAt=70 == deadline
	t.Logf("nextAt=70 == deadline -> %+v; 依据: 恰等于截止允许", r)
	wantResult(t, r, "push", 70)
	r = failOK(t, s, "eq", 70, Transient, 0) // w=80, nextAt=150 > 70
	t.Logf("nextAt=150 > deadline -> %+v", r)
	wantDead(t, r, ReasonExpired)

	mustSubmit(t, s, "plus1", []string{"push"}, 70, 9) // deadline=79
	r = failOK(t, s, "plus1", 70, Transient, 0)        // w=10, nextAt=80 == deadline+1
	t.Logf("nextAt=80 == deadline+1 -> %+v; 依据: 超期即死信", r)
	wantDead(t, r, ReasonExpired)
}

// gfail 恰等于 K 熔断；Success 清零 gfail 但不解除熔断。
func TestCircuitAndSuccessReset(t *testing.T) {
	cfg := Config{Base: 10, Cap: 1000, M: 5, A: 100, RAcap: 0, K: 2, Cool: 100}
	s := mustNew(t, cfg, constJitter(0))
	mustSubmit(t, s, "t1", []string{"push"}, 0, 100000)
	failOK(t, s, "t1", 0, Transient, 0) // gfail[push]=1，未熔断
	if s.openUntil["push"] != 0 {
		t.Fatalf("gfail=1 < K=2 不应熔断, openUntil=%d", s.openUntil["push"])
	}
	failOK(t, s, "t1", 10, Transient, 0) // gfail[push]=2 == K -> openUntil=110
	if got := s.openUntil["push"]; got != 110 {
		t.Fatalf("gfail==K 应熔断至 110, got %d", got)
	}

	mustSubmit(t, s, "t2", []string{"push"}, 30, 100000)
	if err := s.Success("t2", 30); err != nil {
		t.Fatalf("Success: %v", err)
	}
	if got := s.gfail["push"]; got != 0 {
		t.Fatalf("Success 后 gfail=%d, want 0", got)
	}
	if got := s.openUntil["push"]; got != 110 {
		t.Fatalf("Success 不应解除熔断, openUntil=%d, want 110", got)
	}

	// 熔断未解除：切换时跳过 push，无后续渠道则死信。
	mustSubmit(t, s, "t3", []string{"sms", "push"}, 40, 100000)
	r := failOK(t, s, "t3", 40, Permanent, 0)
	t.Logf("push 熔断中切换 -> %+v; 依据: openUntil=110 > 40", r)
	wantDead(t, r, ReasonPermanent)

	// gfail 已清零：熔断期后一次失败不触发新熔断。
	mustSubmit(t, s, "t4", []string{"push"}, 120, 100000)
	failOK(t, s, "t4", 120, Transient, 0)
	if got := s.gfail["push"]; got != 1 {
		t.Fatalf("gfail=%d, want 1 (Success 已清零)", got)
	}
	if got := s.openUntil["push"]; got != 110 {
		t.Fatalf("openUntil=%d, want 110 (未重新熔断)", got)
	}
}

// permanent 不累计 gfail。
func TestPermanentNoGfail(t *testing.T) {
	cfg := Config{Base: 10, Cap: 1000, M: 5, A: 100, RAcap: 0, K: 2, Cool: 100}
	s := mustNew(t, cfg, constJitter(0))
	mustSubmit(t, s, "t", []string{"push", "sms"}, 0, 100000)
	failOK(t, s, "t", 0, Permanent, 0)
	if got := s.gfail["push"]; got != 0 {
		t.Fatalf("permanent 后 gfail=%d, want 0", got)
	}
	failOK(t, s, "t", 0, Throttled, 10) // throttled 累计
	if got := s.gfail["sms"]; got != 1 {
		t.Fatalf("throttled 后 gfail=%d, want 1", got)
	}
}

// 拒绝原因与顺序：参数非法 > 任务不存在 > 已结束 > 时钟回退 > 过早。
func TestRejectOrder(t *testing.T) {
	cfg := Config{Base: 10, Cap: 1000, M: 5, A: 100, RAcap: 60, K: 1000, Cool: 1}
	s := mustNew(t, cfg, constJitter(0))
	mustSubmit(t, s, "t", []string{"push"}, 10, 100000) // nextAt=10
	failOK(t, s, "t", 10, Transient, 0)                 // maxNow=10, nextAt=20

	// 参数非法（class 非法 / ra 越界 / 非 throttled 的 ra 非零 / now 越界），优先于不存在。
	_, err := s.Fail("ghost", 10, "bogus", 0)
	wantErr(t, err, ErrInvalidParam)
	_, err = s.Fail("ghost", 10, Throttled, 1_000_000_001)
	wantErr(t, err, ErrInvalidParam)
	_, err = s.Fail("ghost", 10, Transient, 5)
	wantErr(t, err, ErrInvalidParam)
	_, err = s.Fail("ghost", -1, Transient, 0)
	wantErr(t, err, ErrInvalidParam)
	wantErr(t, s.Success("ghost", -1), ErrInvalidParam)

	// 任务不存在，优先于时钟回退。
	_, err = s.Fail("ghost", 0, Transient, 0)
	wantErr(t, err, ErrNotFound)

	// 已结束，优先于时钟回退。
	mustSubmit(t, s, "dead", []string{"push"}, 10, 100000)
	r := failOK(t, s, "dead", 10, Permanent, 0) // maxNow=10
	wantDead(t, r, ReasonPermanent)
	_, err = s.Fail("dead", 0, Transient, 0)
	wantErr(t, err, ErrFinished)
	wantErr(t, s.Success("dead", 0), ErrFinished)

	// 时钟回退，优先于过早。
	_, err = s.Fail("t", 5, Transient, 0) // 5 < maxNow=10 且 5 < nextAt=20
	wantErr(t, err, ErrClockBackwards)
	wantErr(t, s.Success("t", 5), ErrClockBackwards)

	// 过早。
	_, err = s.Fail("t", 15, Transient, 0) // 15 < nextAt=20
	wantErr(t, err, ErrTooEarly)
	wantErr(t, s.Success("t", 15), ErrTooEarly)
}

// 被拒绝的操作不改变任何任务、gfail、openUntil 与最大 now。
func TestRejectNoStateChange(t *testing.T) {
	cfg := Config{Base: 10, Cap: 1000, M: 5, A: 100, RAcap: 60, K: 2, Cool: 100}
	s := mustNew(t, cfg, constJitter(0))
	mustSubmit(t, s, "t", []string{"push", "sms"}, 0, 100000)
	failOK(t, s, "t", 0, Transient, 0) // gfail[push]=1, nextAt=10, maxNow=0
	snap := fmt.Sprintf("tasks=%v gfail=%v open=%v maxNow=%d",
		s.tasks, s.gfail, s.openUntil, s.maxNow)

	rejected := []func() error{
		func() error { _, e := s.Fail("t", 0, "bogus", 0); return e },
		func() error { _, e := s.Fail("t", 0, Transient, 1); return e },
		func() error { _, e := s.Fail("ghost", 0, Transient, 0); return e },
		func() error { _, e := s.Fail("t", 5, Transient, 0); return e },
		func() error { return s.Success("t", 5) },
		func() error { return s.Submit("", []string{"push"}, 0, 10) },
		func() error { return s.Submit("t", []string{"push"}, 0, 10) },
	}
	for i, op := range rejected {
		if err := op(); err == nil {
			t.Fatalf("op %d should be rejected", i)
		}
		got := fmt.Sprintf("tasks=%v gfail=%v open=%v maxNow=%d",
			s.tasks, s.gfail, s.openUntil, s.maxNow)
		if got != snap {
			t.Fatalf("op %d changed state:\nbefore %s\nafter  %s", i, snap, got)
		}
	}
}

// Submit 的参数校验与重复登记。
func TestSubmitValidation(t *testing.T) {
	cfg := Config{Base: 10, Cap: 1000, M: 5, A: 100, RAcap: 60, K: 1000, Cool: 1}
	s := mustNew(t, cfg, constJitter(0))
	cases := []struct {
		id      string
		chain   []string
		created int64
		ttl     int64
	}{
		{"", []string{"push"}, 0, 10},                   // 空 id
		{"x", nil, 0, 10},                               // 空链
		{"x", []string{"a", "b", "c", "d", "e"}, 0, 10}, // 链过长
		{"x", []string{""}, 0, 10},                      // 空渠道名
		{"x", []string{"a", "a"}, 0, 10},                // 重复渠道
		{"x", []string{"a"}, -1, 10},                    // created 越界
		{"x", []string{"a"}, 1_000_000_000_001, 10},     // created 越界
		{"x", []string{"a"}, 0, 0},                      // ttl 越界
		{"x", []string{"a"}, 0, 1_000_000_001},          // ttl 越界
	}
	for i, c := range cases {
		if err := s.Submit(c.id, c.chain, c.created, c.ttl); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("case %d: got %v, want ErrInvalidParam", i, err)
		}
	}
	mustSubmit(t, s, "ok", []string{"a"}, 0, 10)
	wantErr(t, s.Submit("ok", []string{"b"}, 0, 10), ErrDuplicate)
}

// 构造参数越界。
func TestConfigValidation(t *testing.T) {
	good := Config{Base: 10, Cap: 1000, M: 5, A: 100, RAcap: 60, K: 2, Cool: 100}
	bads := []Config{
		{Base: 0, Cap: 1000, M: 5, A: 100, RAcap: 60, K: 2, Cool: 100},
		{Base: 1_000_001, Cap: 1_000_001, M: 5, A: 100, RAcap: 60, K: 2, Cool: 100},
		{Base: 10, Cap: 9, M: 5, A: 100, RAcap: 60, K: 2, Cool: 100},
		{Base: 10, Cap: 1000, M: 0, A: 100, RAcap: 60, K: 2, Cool: 100},
		{Base: 10, Cap: 1000, M: 17, A: 100, RAcap: 60, K: 2, Cool: 100},
		{Base: 10, Cap: 1000, M: 5, A: 0, RAcap: 60, K: 2, Cool: 100},
		{Base: 10, Cap: 1000, M: 5, A: 101, RAcap: 60, K: 2, Cool: 100},
		{Base: 10, Cap: 1000, M: 5, A: 100, RAcap: -1, K: 2, Cool: 100},
		{Base: 10, Cap: 1000, M: 5, A: 100, RAcap: 60, K: 0, Cool: 100},
		{Base: 10, Cap: 1000, M: 5, A: 100, RAcap: 60, K: 1001, Cool: 100},
		{Base: 10, Cap: 1000, M: 5, A: 100, RAcap: 60, K: 2, Cool: 0},
	}
	for i, c := range bads {
		if _, err := New(c, constJitter(0)); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("case %d (%+v): want ErrInvalidParam", i, c)
		}
	}
	if _, err := New(good, nil); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("nil jitter: want ErrInvalidParam")
	}
}

// 并发调用等价于某个串行顺序：不变量始终成立。
func TestConcurrency(t *testing.T) {
	cfg := Config{Base: 10, Cap: 1000, M: 3, A: 20, RAcap: 60, K: 3, Cool: 50}
	s := mustNew(t, cfg, constJitter(1))
	const tasks = 32
	for i := 0; i < tasks; i++ {
		id := fmt.Sprintf("t%d", i)
		mustSubmit(t, s, id, []string{"push", "sms"}, 0, 100000)
	}
	var wg sync.WaitGroup
	for i := 0; i < tasks; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("t%d", i)
			now := int64(0)
			for step := 0; step < 50; step++ {
				r, err := s.Fail(id, now, Transient, 0)
				if err != nil {
					continue // 被拒绝（过早等），换个时刻重试
				}
				if r.Dead {
					return
				}
				now = r.NextAt
			}
			_ = s.Success(id, now)
		}(i)
	}
	wg.Wait()
	// 不变量：att <= A、n < M、cur 单调（此处只检查终态范围）、恰结束一次。
	for id, tk := range s.tasks {
		if tk.att > cfg.A || tk.n >= cfg.M || tk.cur < 0 || tk.cur >= len(tk.chain) {
			t.Fatalf("task %s violates invariant: %+v", id, tk)
		}
	}
}
