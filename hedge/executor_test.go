package hedge

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)

// gate 是测试脚本下发的单个副本应答。
type gate struct {
	value any
	err   error
}

// script 记录发送序列，并按测试脚本控制每个副本的应答与取消。
type script struct {
	sends     chan string
	cancelled chan string
	gates     map[string]chan gate
}

func newScript(replicas ...string) *script {
	s := &script{
		sends:     make(chan string, 16),
		cancelled: make(chan string, 16),
		gates:     make(map[string]chan gate),
	}
	for _, r := range replicas {
		s.gates[r] = make(chan gate, 1)
	}
	return s
}

func (s *script) send(ctx context.Context, replica string) (any, error) {
	s.sends <- replica
	select {
	case g := <-s.gates[replica]:
		return g.value, g.err
	case <-ctx.Done():
		s.cancelled <- replica
		return nil, ctx.Err()
	}
}

func (s *script) respond(replica string, g gate) {
	s.gates[replica] <- g
}

func expectSend(t *testing.T, s *script, want string) {
	t.Helper()
	select {
	case got := <-s.sends:
		if got != want {
			t.Fatalf("send order mismatch: got %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for send to %q", want)
	}
}

func expectNoSend(t *testing.T, s *script, d time.Duration) {
	t.Helper()
	select {
	case got := <-s.sends:
		t.Fatalf("unexpected send to %q", got)
	case <-time.After(d):
	}
}

func expectCancelled(t *testing.T, s *script, want string) {
	t.Helper()
	select {
	case got := <-s.cancelled:
		if got != want {
			t.Fatalf("cancel mismatch: got %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for cancel of %q", want)
	}
}

func runAsync(exec *Executor, req Request, send SendFunc) <-chan Result {
	ch := make(chan Result, 1)
	go func() {
		res, err := exec.Execute(context.Background(), req, send)
		if err != nil {
			panic(fmt.Sprintf("unexpected validation error: %v", err))
		}
		ch <- res
	}()
	return ch
}

func awaitResult(t *testing.T, ch <-chan Result) Result {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for result")
		return Result{}
	}
}

// 慢主请求被备份请求胜出：首个成功被采用，其余在途取消，迟到结果丢弃。
func TestSlowPrimaryBeatenByBackup(t *testing.T) {
	clock := NewFakeClock(t0)
	exec := NewExecutor(clock, 10, 0)
	s := newScript("a", "b", "c")
	req := Request{
		Replicas:    []string{"a", "b", "c"},
		HedgeDelay:  time.Second,
		MaxInFlight: 3,
		Deadline:    t0.Add(10 * time.Second),
		Idempotent:  true,
	}
	t.Logf("输入: replicas=%v delay=%v maxInFlight=%d deadline=%v", req.Replicas, req.HedgeDelay, req.MaxInFlight, req.Deadline)

	resCh := runAsync(exec, req, s.send)
	expectSend(t, s, "a")

	clock.Advance(time.Second) // 满一个对冲延迟且无成功 -> 对冲 b
	expectSend(t, s, "b")

	s.respond("b", gate{value: "ok-b"})
	res := awaitResult(t, resCh)
	t.Logf("输出: outcome=%v replica=%q value=%v", res.Outcome, res.Replica, res.Value)

	if res.Outcome != OutcomeSuccess || res.Replica != "b" || res.Value != "ok-b" {
		t.Fatalf("unexpected result: %+v", res)
	}
	expectCancelled(t, s, "a") // 其余在途请求被取消

	// 迟到结果丢弃：结局不变，统计不变。
	s.respond("a", gate{value: "late-a"})
	s.respond("c", gate{value: "late-c"})
	stats := exec.Stats()
	t.Logf("判定依据: 首个成功来自 b 即定局；a 被取消；迟到结果被丢弃；hedges=%d", stats.Hedges)
	if stats.Hedges != 1 {
		t.Fatalf("hedges = %d, want 1", stats.Hedges)
	}
}

// 主请求快速失败立即改投下一副本：计为重试不占对冲预算，且重新开始对冲计时。
func TestPrimaryFastFailImmediateRetry(t *testing.T) {
	clock := NewFakeClock(t0)
	exec := NewExecutor(clock, 10, 0)
	s := newScript("a", "b", "c")
	req := Request{
		Replicas:    []string{"a", "b", "c"},
		HedgeDelay:  time.Second,
		MaxInFlight: 2,
		Deadline:    t0.Add(10 * time.Second),
		Idempotent:  true,
	}
	t.Logf("输入: replicas=%v delay=%v maxInFlight=%d", req.Replicas, req.HedgeDelay, req.MaxInFlight)

	resCh := runAsync(exec, req, s.send)
	expectSend(t, s, "a")

	s.respond("a", gate{err: errors.New("boom")}) // t0 时刻失败
	expectSend(t, s, "b")                         // 立即改投 b：重试而非对冲
	if got := exec.Stats().Hedges; got != 0 {
		t.Fatalf("hedges = %d, want 0 (retry must not consume hedge budget)", got)
	}

	clock.Advance(time.Second) // 自重试发出起满一个延迟 -> 对冲 c
	expectSend(t, s, "c")

	s.respond("c", gate{value: "ok-c"})
	res := awaitResult(t, resCh)
	t.Logf("输出: outcome=%v replica=%q value=%v", res.Outcome, res.Replica, res.Value)
	t.Logf("判定依据: 发送序列 a->b(重试)->c(对冲)，重试不计预算，hedges=%d", exec.Stats().Hedges)

	if res.Outcome != OutcomeSuccess || res.Replica != "c" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if got := exec.Stats().Hedges; got != 1 {
		t.Fatalf("hedges = %d, want 1", got)
	}
}

// 预算耗尽时不再对冲：每个检查周期都跳过，主请求仍可自行成功。
func TestBudgetExhaustedNoHedge(t *testing.T) {
	clock := NewFakeClock(t0)
	exec := NewExecutor(clock, 0, 0) // 预算恒为 0
	s := newScript("a", "b")
	req := Request{
		Replicas:    []string{"a", "b"},
		HedgeDelay:  time.Second,
		MaxInFlight: 2,
		Deadline:    t0.Add(10 * time.Second),
		Idempotent:  true,
	}
	t.Logf("输入: baseCap=0 ratio=0 replicas=%v delay=%v", req.Replicas, req.HedgeDelay)

	resCh := runAsync(exec, req, s.send)
	expectSend(t, s, "a")

	clock.Advance(time.Second) // 检查 1：预算不足，跳过
	clock.Advance(time.Second) // 检查 2：预算不足，跳过
	expectNoSend(t, s, 100*time.Millisecond)

	s.respond("a", gate{value: "ok-a"})
	res := awaitResult(t, resCh)
	t.Logf("输出: outcome=%v replica=%q", res.Outcome, res.Replica)
	t.Logf("判定依据: 预算 floor(0+accepted*0)=0，两次检查均跳过，hedges=%d", exec.Stats().Hedges)

	if res.Outcome != OutcomeSuccess || res.Replica != "a" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if got := exec.Stats().Hedges; got != 0 {
		t.Fatalf("hedges = %d, want 0", got)
	}
}

// 同一应答脚本与时钟推进反复执行，发送序列与结局完全相同。
func TestDeterministicReplay(t *testing.T) {
	const runs = 20
	var wantSends []string
	var wantResult Result

	for i := 0; i < runs; i++ {
		clock := NewFakeClock(t0)
		exec := NewExecutor(clock, 5, 0)
		s := newScript("a", "b", "c", "d")
		req := Request{
			Replicas:    []string{"a", "b", "c", "d"},
			HedgeDelay:  time.Second,
			MaxInFlight: 3,
			Deadline:    t0.Add(30 * time.Second),
			Idempotent:  true,
		}

		resCh := runAsync(exec, req, s.send)
		var sends []string
		record := func(want string) {
			expectSend(t, s, want)
			sends = append(sends, want)
		}
		record("a")
		clock.Advance(time.Second) // 对冲 b
		record("b")
		s.respond("b", gate{err: errors.New("boom")}) // 重试 c
		record("c")
		clock.Advance(time.Second) // 自重试起满一个延迟，对冲 d
		record("d")
		s.respond("d", gate{value: "ok-d"})
		res := awaitResult(t, resCh)

		if i == 0 {
			wantSends, wantResult = sends, res
			t.Logf("输入: replicas=%v delay=%v；脚本: 对冲b, b失败重试c, 对冲d, d成功", req.Replicas, req.HedgeDelay)
			t.Logf("输出: sends=%v outcome=%v replica=%q", sends, res.Outcome, res.Replica)
			continue
		}
		if !reflect.DeepEqual(sends, wantSends) || !reflect.DeepEqual(res, wantResult) {
			t.Fatalf("run %d diverged: sends=%v result=%+v, want sends=%v result=%+v",
				i, sends, res, wantSends, wantResult)
		}
	}
	t.Logf("判定依据: %d 次重放的发送序列与结局完全一致", runs)
}

// 百个并发调用共享预算：任意时刻 hedges <= floor(baseCap + accepted*ratio)，
// 且每次调用恰好得到一个结局。
func TestConcurrentSharedBudget(t *testing.T) {
	const (
		calls   = 100
		baseCap = 10
		ratio   = 0.5
	)
	exec := NewExecutor(RealClock(), baseCap, ratio)

	// 监视器：持续抽样核对预算不变式。
	violations := make(chan string, 1)
	stop := make(chan struct{})
	var monitorWg sync.WaitGroup
	monitorWg.Add(1)
	go func() {
		defer monitorWg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			st := exec.Stats()
			if st.Hedges > exec.Budget() {
				select {
				case violations <- fmt.Sprintf("hedges=%d exceeds budget", st.Hedges):
				default:
				}
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()

	// 发送函数阻塞到被取消为止，迫使各调用不断尝试对冲。
	blockUntilCancel := func(ctx context.Context, replica string) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	results := make([]Result, calls)
	var wg sync.WaitGroup
	for i := 0; i < calls; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := Request{
				Replicas:    []string{fmt.Sprintf("r%d-0", i), fmt.Sprintf("r%d-1", i), fmt.Sprintf("r%d-2", i), fmt.Sprintf("r%d-3", i), fmt.Sprintf("r%d-4", i)},
				HedgeDelay:  time.Millisecond,
				MaxInFlight: 5,
				Deadline:    time.Now().Add(150 * time.Millisecond),
				Idempotent:  true,
			}
			res, err := exec.Execute(context.Background(), req, blockUntilCancel)
			if err != nil {
				t.Errorf("call %d: unexpected validation error: %v", i, err)
				return
			}
			results[i] = res // 每次调用恰好写入一个结局
		}(i)
	}
	wg.Wait()
	close(stop)
	monitorWg.Wait()

	select {
	case v := <-violations:
		t.Fatalf("budget invariant violated: %s", v)
	default:
	}

	stats := exec.Stats()
	maxBudget := int64(baseCap) + int64(float64(calls)*ratio)
	t.Logf("输入: calls=%d baseCap=%d ratio=%v", calls, baseCap, ratio)
	t.Logf("输出: accepted=%d hedges=%d", stats.Accepted, stats.Hedges)
	t.Logf("判定依据: hedges(%d) <= floor(%d + %d*%v) = %d，且 %d 个结局全部唯一落定",
		stats.Hedges, baseCap, stats.Accepted, ratio, maxBudget, calls)

	if stats.Accepted != calls {
		t.Fatalf("accepted = %d, want %d", stats.Accepted, calls)
	}
	if stats.Hedges > maxBudget {
		t.Fatalf("hedges = %d exceeds budget %d", stats.Hedges, maxBudget)
	}
	if stats.Hedges == 0 {
		t.Fatal("expected some hedges to be issued")
	}
	for i, res := range results {
		if res.Outcome != OutcomeTimeout {
			t.Fatalf("call %d: outcome = %v, want timeout (sends never succeed)", i, res.Outcome)
		}
	}
}

// 预算随已接受调用数增长：floor(baseCap + accepted*ratio)。
func TestBudgetGrowsWithAcceptedCalls(t *testing.T) {
	clock := NewFakeClock(t0)
	exec := NewExecutor(clock, 0, 1) // 每接受一个调用 +1 对冲额度
	mkReq := func(replicas ...string) Request {
		return Request{
			Replicas:    replicas,
			HedgeDelay:  time.Second,
			MaxInFlight: 3,
			Deadline:    t0.Add(10 * time.Second),
			Idempotent:  true,
		}
	}

	s1 := newScript("a", "b", "c")
	res1 := runAsync(exec, mkReq("a", "b", "c"), s1.send)
	expectSend(t, s1, "a")
	clock.Advance(time.Second) // accepted=1 -> 预算 1，对冲 b
	expectSend(t, s1, "b")
	clock.Advance(time.Second) // 预算耗尽，跳过
	expectNoSend(t, s1, 100*time.Millisecond)
	s1.respond("a", gate{value: "ok-a"})
	if res := awaitResult(t, res1); res.Outcome != OutcomeSuccess {
		t.Fatalf("call 1: unexpected result: %+v", res)
	}

	s2 := newScript("x", "y", "z")
	res2 := runAsync(exec, mkReq("x", "y", "z"), s2.send)
	expectSend(t, s2, "x")
	clock.Advance(time.Second) // accepted=2 -> 预算 2，再对冲一次
	expectSend(t, s2, "y")
	clock.Advance(time.Second) // 预算再次耗尽
	expectNoSend(t, s2, 100*time.Millisecond)
	s2.respond("x", gate{value: "ok-x"})
	if res := awaitResult(t, res2); res.Outcome != OutcomeSuccess {
		t.Fatalf("call 2: unexpected result: %+v", res)
	}

	stats := exec.Stats()
	t.Logf("判定依据: 预算 floor(0+accepted*1)，两次调用各获 1 次对冲，accepted=%d hedges=%d", stats.Accepted, stats.Hedges)
	if stats.Accepted != 2 || stats.Hedges != 2 {
		t.Fatalf("stats = %+v, want accepted=2 hedges=2", stats)
	}
}

// 非幂等请求只发往首个副本：既不对冲也不重试。
func TestNonIdempotentNeverResent(t *testing.T) {
	clock := NewFakeClock(t0)
	exec := NewExecutor(clock, 10, 0)
	s := newScript("a", "b", "c")
	req := Request{
		Replicas:    []string{"a", "b", "c"},
		HedgeDelay:  time.Second,
		MaxInFlight: 3,
		Deadline:    t0.Add(10 * time.Second),
		Idempotent:  false,
	}
	t.Logf("输入: idempotent=false replicas=%v delay=%v", req.Replicas, req.HedgeDelay)

	resCh := runAsync(exec, req, s.send)
	expectSend(t, s, "a")

	clock.Advance(3 * time.Second) // 多个对冲周期过去，不应有任何对冲
	expectNoSend(t, s, 100*time.Millisecond)

	s.respond("a", gate{err: errors.New("boom")}) // 失败也不重试
	res := awaitResult(t, resCh)
	expectNoSend(t, s, 100*time.Millisecond)
	t.Logf("输出: outcome=%v errors=%v", res.Outcome, res.Errors)
	t.Logf("判定依据: 非幂等请求永不重发，唯一副本失败即全部失败，hedges=%d", exec.Stats().Hedges)

	if res.Outcome != OutcomeAllFailed {
		t.Fatalf("outcome = %v, want all-failed", res.Outcome)
	}
	if len(res.Errors) != 1 || res.Errors[0].Replica != "a" {
		t.Fatalf("errors = %+v, want single error from a", res.Errors)
	}
	if got := exec.Stats().Hedges; got != 0 {
		t.Fatalf("hedges = %d, want 0", got)
	}
}

// 恰在截止时刻到达的成功视为超时。
func TestSuccessAtDeadlineIsTimeout(t *testing.T) {
	clock := NewFakeClock(t0)
	exec := NewExecutor(clock, 10, 0)
	s := newScript("a", "b")
	req := Request{
		Replicas:    []string{"a", "b"},
		HedgeDelay:  time.Second,
		MaxInFlight: 2,
		Deadline:    t0.Add(5 * time.Second),
		Idempotent:  true,
	}
	t.Logf("输入: deadline=%v", req.Deadline)

	resCh := runAsync(exec, req, s.send)
	expectSend(t, s, "a")

	clock.Advance(5 * time.Second) // 时钟恰好走到截止时刻
	s.respond("a", gate{value: "too-late"})
	res := awaitResult(t, resCh)
	t.Logf("输出: outcome=%v", res.Outcome)
	t.Logf("判定依据: 成功到达时刻 == 截止时刻，按超时处理")

	if res.Outcome != OutcomeTimeout {
		t.Fatalf("outcome = %v, want timeout", res.Outcome)
	}
}

// 全部失败时按发出顺序返回各副本错误（与失败到达顺序无关）。
func TestAllFailedErrorsInSendOrder(t *testing.T) {
	clock := NewFakeClock(t0)
	exec := NewExecutor(clock, 10, 0)
	s := newScript("a", "b", "c")
	req := Request{
		Replicas:    []string{"a", "b", "c"},
		HedgeDelay:  time.Second,
		MaxInFlight: 3,
		Deadline:    t0.Add(10 * time.Second),
		Idempotent:  true,
	}
	t.Logf("输入: replicas=%v maxInFlight=%d", req.Replicas, req.MaxInFlight)

	resCh := runAsync(exec, req, s.send)
	expectSend(t, s, "a")
	clock.Advance(time.Second)
	expectSend(t, s, "b")
	clock.Advance(time.Second)
	expectSend(t, s, "c")

	// 以与发出顺序相反的次序失败。
	s.respond("c", gate{err: errors.New("err-c")})
	s.respond("a", gate{err: errors.New("err-a")})
	s.respond("b", gate{err: errors.New("err-b")})
	res := awaitResult(t, resCh)
	t.Logf("输出: outcome=%v errors=%v", res.Outcome, res.Errors)

	if res.Outcome != OutcomeAllFailed {
		t.Fatalf("outcome = %v, want all-failed", res.Outcome)
	}
	var order []string
	for _, e := range res.Errors {
		order = append(order, e.Replica)
	}
	t.Logf("判定依据: 错误按发出顺序排列 %v（到达顺序为 c,a,b）", order)
	if !reflect.DeepEqual(order, []string{"a", "b", "c"}) {
		t.Fatalf("error order = %v, want [a b c]", order)
	}
}

// 非法输入在发出任何请求前整体拒绝，且原因可区分。
func TestValidationRejectsBeforeAnySend(t *testing.T) {
	clock := NewFakeClock(t0)
	exec := NewExecutor(clock, 10, 0)
	valid := Request{
		Replicas:    []string{"a", "b"},
		HedgeDelay:  time.Second,
		MaxInFlight: 2,
		Deadline:    t0.Add(10 * time.Second),
		Idempotent:  true,
	}
	cases := []struct {
		name   string
		mutate func(*Request)
		want   error
	}{
		{"empty replicas", func(r *Request) { r.Replicas = nil }, ErrNoReplicas},
		{"duplicate replicas", func(r *Request) { r.Replicas = []string{"a", "a"} }, ErrDuplicateReplica},
		{"zero hedge delay", func(r *Request) { r.HedgeDelay = 0 }, ErrNonPositiveHedgeDelay},
		{"negative hedge delay", func(r *Request) { r.HedgeDelay = -time.Second }, ErrNonPositiveHedgeDelay},
		{"zero max in-flight", func(r *Request) { r.MaxInFlight = 0 }, ErrNonPositiveMaxInFlight},
		{"deadline passed", func(r *Request) { r.Deadline = t0.Add(-time.Second) }, ErrDeadlinePassed},
		{"deadline is now", func(r *Request) { r.Deadline = t0 }, ErrDeadlinePassed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := valid
			tc.mutate(&req)
			s := newScript("a", "b")
			_, err := exec.Execute(context.Background(), req, s.send)
			t.Logf("输入: %+v", req)
			t.Logf("输出: err=%v", err)
			t.Logf("判定依据: errors.Is(err, %v) 成立且未发出任何请求", tc.want)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			expectNoSend(t, s, 50*time.Millisecond)
		})
	}
	stats := exec.Stats()
	if stats.Accepted != 0 || stats.Hedges != 0 {
		t.Fatalf("rejected calls must not be accepted: stats = %+v", stats)
	}
}

// 在途数已满时跳过对冲检查。
func TestMaxInFlightSkipsHedge(t *testing.T) {
	clock := NewFakeClock(t0)
	exec := NewExecutor(clock, 10, 0)
	s := newScript("a", "b")
	req := Request{
		Replicas:    []string{"a", "b"},
		HedgeDelay:  time.Second,
		MaxInFlight: 1,
		Deadline:    t0.Add(10 * time.Second),
		Idempotent:  true,
	}
	t.Logf("输入: maxInFlight=1 replicas=%v", req.Replicas)

	resCh := runAsync(exec, req, s.send)
	expectSend(t, s, "a")

	clock.Advance(2 * time.Second) // 两个检查周期：在途数已满，均跳过
	expectNoSend(t, s, 100*time.Millisecond)

	s.respond("a", gate{value: "ok-a"})
	res := awaitResult(t, resCh)
	t.Logf("输出: outcome=%v replica=%q", res.Outcome, res.Replica)
	t.Logf("判定依据: inFlight(1) 不小于 maxInFlight(1)，对冲被跳过，hedges=%d", exec.Stats().Hedges)

	if res.Outcome != OutcomeSuccess || res.Replica != "a" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if got := exec.Stats().Hedges; got != 0 {
		t.Fatalf("hedges = %d, want 0", got)
	}
}
