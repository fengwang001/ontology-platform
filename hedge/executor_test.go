package hedge

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

var (
	errBoom0 = errors.New("boom-0")
	errBoom1 = errors.New("boom-1")
	errBoom2 = errors.New("boom-2")
)

func okResp(v string) Response { return Response{Value: v} }

func failResp(err error) Response { return Response{Err: err} }

func baseSpec(env *testEnv, replicas ...string) Spec {
	return Spec{
		Replicas:    replicas,
		HedgeDelay:  100 * time.Millisecond,
		MaxInFlight: 3,
		Deadline:    env.start.Add(5 * time.Second),
		Idempotent:  true,
	}
}

// 百个并发调用共享预算：已发出对冲总数始终不超过
// 固定额度 + floor(比例 * 已接受调用数)，且每次调用恰好一个结局。
func TestConcurrentCallsShareBudget(t *testing.T) {
	const n = 100
	scripts := map[string]script{}
	for i := 0; i < n; i++ {
		scripts[fmt.Sprintf("c%dr0", i)] = never()
		scripts[fmt.Sprintf("c%dr1", i)] = after(50*time.Millisecond, okResp(fmt.Sprintf("win-%d", i)))
		scripts[fmt.Sprintf("c%dr2", i)] = never()
	}
	env := newEnv(t, 5, 0.1, scripts) // 额度 = 5 + floor(0.1*100) = 15

	handles := make([]*CallHandle, n)
	for i := 0; i < n; i++ {
		spec := baseSpec(env,
			fmt.Sprintf("c%dr0", i), fmt.Sprintf("c%dr1", i), fmt.Sprintf("c%dr2", i))
		spec.HedgeDelay = 10 * time.Millisecond
		spec.Deadline = env.start.Add(2 * time.Second)
		handles[i] = env.executor.Start(spec)
	}

	env.clock.Advance(10 * time.Millisecond)            // 100 个对冲检查点竞争 15 个额度
	env.clock.Advance(50 * time.Millisecond)            // 胜出的 15 个备份请求成功
	env.clock.AdvanceTo(env.start.Add(2 * time.Second)) // 其余到达截止时间

	success, timeout, other := 0, 0, 0
	for i, h := range handles {
		o := h.Wait() // 每次调用恰好返回一个结局
		switch o.Kind {
		case Success:
			success++
			if o.Value != fmt.Sprintf("win-%d", i) {
				t.Fatalf("call %d 采用了错误的值: %+v", i, o)
			}
		case Timeout:
			timeout++
		default:
			other++
		}
	}
	if success != 15 || timeout != 85 || other != 0 {
		t.Fatalf("结局统计不符: success=%d timeout=%d other=%d", success, timeout, other)
	}

	accepted, hedges, allowance, violated := env.budget.Snapshot()
	if accepted != n || hedges != 15 || allowance != 15 || violated {
		t.Fatalf("预算核对失败: accepted=%d hedges=%d allowance=%d violated=%v",
			accepted, hedges, allowance, violated)
	}
	if got := len(env.transport.sendLog()); got != n+15 {
		t.Fatalf("发送总数应为 100 首次 + 15 对冲, got=%d", got)
	}
	assertLogContains(t, env, "reason=budget-exhausted", "kind=hedge")
}

// 同一副本应答脚本与时钟推进反复执行，发送序列与结局完全相同。
func TestReplayDeterministic(t *testing.T) {
	run := func() ([]sendEvent, []string) {
		scripts := map[string]script{
			"a0": after(300*time.Millisecond, okResp("a0-slow")),
			"a1": after(100*time.Millisecond, okResp("a1-fast")),
			"b0": after(50*time.Millisecond, failResp(errBoom0)),
			"b1": after(50*time.Millisecond, failResp(errBoom1)),
			"c0": never(),
			"c1": never(),
		}
		env := newEnv(t, 10, 0, scripts)
		ha := env.executor.Start(baseSpec(env, "a0", "a1"))
		hb := env.executor.Start(baseSpec(env, "b0", "b1"))
		hc := env.executor.Start(baseSpec(env, "c0", "c1"))
		for i := 0; i < 10; i++ {
			env.clock.Advance(500 * time.Millisecond)
		}
		outcomes := []string{
			fmt.Sprintf("%+v", ha.Wait()),
			fmt.Sprintf("%+v", hb.Wait()),
			fmt.Sprintf("%+v", hc.Wait()),
		}
		return env.transport.sendLog(), outcomes
	}

	sends1, outcomes1 := run()
	for i := 0; i < 3; i++ {
		sends2, outcomes2 := run()
		if !reflect.DeepEqual(sends1, sends2) {
			t.Fatalf("第 %d 次重放发送序列不同:\n%v\n%v", i, sends1, sends2)
		}
		if !reflect.DeepEqual(outcomes1, outcomes2) {
			t.Fatalf("第 %d 次重放结局不同:\n%v\n%v", i, outcomes1, outcomes2)
		}
	}
}

func assertSends(t *testing.T, env *testEnv, want []sendEvent) {
	t.Helper()
	got := env.transport.sendLog()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("发送序列不符:\n got=%+v\nwant=%+v", got, want)
	}
}

func assertLogContains(t *testing.T, env *testEnv, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if !strings.Contains(env.logs.String(), s) {
			t.Fatalf("日志缺少判定依据 %q", s)
		}
	}
}

// 慢主请求被备份请求胜出：首个副本迟迟不返回，对冲副本先成功。
func TestSlowPrimaryHedgeWins(t *testing.T) {
	env := newEnv(t, 10, 0, map[string]script{
		"r0": after(500*time.Millisecond, okResp("slow")),
		"r1": after(150*time.Millisecond, okResp("fast")),
	})
	h := env.executor.Start(baseSpec(env, "r0", "r1", "r2"))

	env.clock.Advance(100 * time.Millisecond) // 对冲计时到点 -> 发 r1
	assertSends(t, env, []sendEvent{
		{replica: "r0", at: env.start},
		{replica: "r1", at: env.start.Add(100 * time.Millisecond)},
	})

	env.clock.Advance(150 * time.Millisecond) // 200ms 处再对冲 r2; 250ms 处 r1 成功
	assertSends(t, env, []sendEvent{
		{replica: "r0", at: env.start},
		{replica: "r1", at: env.start.Add(100 * time.Millisecond)},
		{replica: "r2", at: env.start.Add(200 * time.Millisecond)},
	})
	o := h.Wait()
	if o.Kind != Success || o.Value != "fast" || o.Replica != "r1" {
		t.Fatalf("结局不符: %+v", o)
	}
	if o.Hedges != 2 || o.Retries != 0 {
		t.Fatalf("对冲/重试计数不符: %+v", o)
	}
	if got := env.transport.cancelLog(); !reflect.DeepEqual(got, []string{"r0", "r2"}) {
		t.Fatalf("应按发出顺序取消在途的 r0/r2, got=%v", got)
	}

	env.clock.Advance(300 * time.Millisecond) // r0 在 500ms 处迟到
	assertLogContains(t, env, "kind=hedge", "adopt success: replica=r1", "late response discarded")
}

// 主请求快速失败：立即改投下一副本，计为重试而不占对冲预算。
func TestPrimaryFailsFastRetry(t *testing.T) {
	env := newEnv(t, 10, 0, map[string]script{
		"r0": after(50*time.Millisecond, failResp(errBoom0)),
		"r1": after(100*time.Millisecond, okResp("retried")),
	})
	spec := baseSpec(env, "r0", "r1", "r2")
	spec.HedgeDelay = 200 * time.Millisecond // 失败先于对冲计时
	h := env.executor.Start(spec)

	env.clock.Advance(50 * time.Millisecond) // r0 失败 -> 立即重试 r1
	assertSends(t, env, []sendEvent{
		{replica: "r0", at: env.start},
		{replica: "r1", at: env.start.Add(50 * time.Millisecond)},
	})

	env.clock.Advance(100 * time.Millisecond) // r1 在 150ms 处成功
	o := h.Wait()
	if o.Kind != Success || o.Value != "retried" || o.Replica != "r1" {
		t.Fatalf("结局不符: %+v", o)
	}
	if o.Hedges != 0 || o.Retries != 1 {
		t.Fatalf("重试不应占对冲预算: %+v", o)
	}
	_, hedges, _, _ := env.budget.Snapshot()
	if hedges != 0 {
		t.Fatalf("预算不应被重试消耗, hedges=%d", hedges)
	}
	assertLogContains(t, env, "retry immediately", "kind=retry")
}

// 预算耗尽时不再对冲：每个对冲检查点都跳过，最终超时。
func TestBudgetExhaustedNoHedge(t *testing.T) {
	env := newEnv(t, 0, 0, map[string]script{
		"r0": never(),
	})
	spec := baseSpec(env, "r0", "r1", "r2")
	spec.Deadline = env.start.Add(1 * time.Second)
	h := env.executor.Start(spec)

	env.clock.Advance(500 * time.Millisecond) // 100~500ms 五个检查点全部跳过
	assertSends(t, env, []sendEvent{{replica: "r0", at: env.start}})

	env.clock.AdvanceTo(env.start.Add(1 * time.Second))
	o := h.Wait()
	if o.Kind != Timeout || !errors.Is(o.Reason, ErrTimeout) {
		t.Fatalf("结局应为超时: %+v", o)
	}
	assertSends(t, env, []sendEvent{{replica: "r0", at: env.start}})
	_, hedges, _, violated := env.budget.Snapshot()
	if hedges != 0 || violated {
		t.Fatalf("预算不应被消耗或违反: hedges=%d violated=%v", hedges, violated)
	}
	assertLogContains(t, env, "reason=budget-exhausted", "deadline reached")
}

// 非幂等请求：只发首个副本，既不对冲也不重试。
func TestNonIdempotent(t *testing.T) {
	t.Run("不对冲", func(t *testing.T) {
		env := newEnv(t, 10, 0, map[string]script{"r0": never()})
		spec := baseSpec(env, "r0", "r1", "r2")
		spec.Idempotent = false
		spec.Deadline = env.start.Add(1 * time.Second)
		h := env.executor.Start(spec)

		env.clock.Advance(500 * time.Millisecond)
		assertSends(t, env, []sendEvent{{replica: "r0", at: env.start}})

		env.clock.AdvanceTo(env.start.Add(1 * time.Second))
		if o := h.Wait(); o.Kind != Timeout {
			t.Fatalf("结局应为超时: %+v", o)
		}
		assertSends(t, env, []sendEvent{{replica: "r0", at: env.start}})
	})

	t.Run("不重试", func(t *testing.T) {
		env := newEnv(t, 10, 0, map[string]script{
			"r0": after(50*time.Millisecond, failResp(errBoom0)),
		})
		spec := baseSpec(env, "r0", "r1", "r2")
		spec.Idempotent = false
		h := env.executor.Start(spec)

		env.clock.Advance(50 * time.Millisecond)
		o := h.Wait()
		if o.Kind != AllFailed || !errors.Is(o.Reason, ErrAllFailed) {
			t.Fatalf("结局应为全部失败: %+v", o)
		}
		if len(o.Errors) != 1 || o.Errors[0].Replica != "r0" || !errors.Is(o.Errors[0].Err, errBoom0) {
			t.Fatalf("错误列表不符: %+v", o.Errors)
		}
		assertSends(t, env, []sendEvent{{replica: "r0", at: env.start}})
	})
}

// 全部失败时按发出顺序返回各副本错误（纯重试链）。
func TestAllFailedErrorsInSendOrder(t *testing.T) {
	env := newEnv(t, 10, 0, map[string]script{
		"r0": after(10*time.Millisecond, failResp(errBoom0)),
		"r1": after(20*time.Millisecond, failResp(errBoom1)),
		"r2": after(30*time.Millisecond, failResp(errBoom2)),
	})
	spec := baseSpec(env, "r0", "r1", "r2")
	spec.HedgeDelay = 5 * time.Second // 不发生对冲，只走重试
	h := env.executor.Start(spec)

	env.clock.Advance(100 * time.Millisecond)
	o := h.Wait()
	if o.Kind != AllFailed {
		t.Fatalf("结局应为全部失败: %+v", o)
	}
	want := []AttemptError{
		{Replica: "r0", Err: errBoom0},
		{Replica: "r1", Err: errBoom1},
		{Replica: "r2", Err: errBoom2},
	}
	if !reflect.DeepEqual(o.Errors, want) {
		t.Fatalf("错误应按发出顺序:\n got=%+v\nwant=%+v", o.Errors, want)
	}
	assertSends(t, env, []sendEvent{
		{replica: "r0", at: env.start},
		{replica: "r1", at: env.start.Add(10 * time.Millisecond)},
		{replica: "r2", at: env.start.Add(30 * time.Millisecond)},
	})
}

// 在途数满时跳过对冲，但失败后的立即重试不受在途上限限制。
func TestMaxInFlightBlocksHedgeButNotRetry(t *testing.T) {
	env := newEnv(t, 10, 0, map[string]script{
		"r0": after(250*time.Millisecond, failResp(errBoom0)),
		"r1": after(50*time.Millisecond, okResp("retried")),
	})
	spec := baseSpec(env, "r0", "r1")
	spec.MaxInFlight = 1
	h := env.executor.Start(spec)

	env.clock.Advance(200 * time.Millisecond) // 100/200ms 检查点因在途满而跳过
	assertSends(t, env, []sendEvent{{replica: "r0", at: env.start}})

	env.clock.Advance(100 * time.Millisecond) // 250ms 失败立即重试 r1, 300ms 成功
	o := h.Wait()
	if o.Kind != Success || o.Replica != "r1" || o.Hedges != 0 || o.Retries != 1 {
		t.Fatalf("结局不符: %+v", o)
	}
	assertSends(t, env, []sendEvent{
		{replica: "r0", at: env.start},
		{replica: "r1", at: env.start.Add(250 * time.Millisecond)},
	})
	assertLogContains(t, env, "reason=inflight-full")
}

// 非法参数在发出任何请求前整体拒绝，且原因可区分。
func TestValidation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Spec, *testEnv)
		want   error
	}{
		{"空副本列表", func(s *Spec, _ *testEnv) { s.Replicas = nil }, ErrNoReplicas},
		{"重复副本", func(s *Spec, _ *testEnv) { s.Replicas = []string{"r0", "r0"} }, ErrDuplicateReplica},
		{"对冲延迟为零", func(s *Spec, _ *testEnv) { s.HedgeDelay = 0 }, ErrNonPositiveHedgeDelay},
		{"对冲延迟为负", func(s *Spec, _ *testEnv) { s.HedgeDelay = -time.Second }, ErrNonPositiveHedgeDelay},
		{"最大在途数为零", func(s *Spec, _ *testEnv) { s.MaxInFlight = 0 }, ErrNonPositiveMaxInFlight},
		{"最大在途数为负", func(s *Spec, _ *testEnv) { s.MaxInFlight = -2 }, ErrNonPositiveMaxInFlight},
		{"截止时间已过", func(s *Spec, env *testEnv) { s.Deadline = env.start.Add(-time.Second) }, ErrDeadlinePassed},
		{"截止时间恰为现在", func(s *Spec, env *testEnv) { s.Deadline = env.start }, ErrDeadlinePassed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newEnv(t, 10, 0, map[string]script{"r0": never(), "r1": never()})
			spec := baseSpec(env, "r0", "r1")
			tc.mutate(&spec, env)
			o := env.executor.Execute(spec)
			if o.Kind != Rejected || !errors.Is(o.Reason, tc.want) {
				t.Fatalf("应拒绝且原因为 %v: %+v", tc.want, o)
			}
			if got := env.transport.sendLog(); len(got) != 0 {
				t.Fatalf("拒绝前不应发出任何请求: %+v", got)
			}
			accepted, _, _, _ := env.budget.Snapshot()
			if accepted != 0 {
				t.Fatalf("被拒绝的调用不应计入已接受数: accepted=%d", accepted)
			}
		})
	}
}

// 迟到结果丢弃：首个成功被采用后，迟到应答不改变结局。
func TestLateResultDiscarded(t *testing.T) {
	env := newEnv(t, 10, 0, map[string]script{
		"r0": after(400*time.Millisecond, okResp("late-r0")),
		"r1": after(200*time.Millisecond, okResp("win-r1")),
	})
	h := env.executor.Start(baseSpec(env, "r0", "r1"))

	env.clock.Advance(100 * time.Millisecond) // 对冲 r1
	env.clock.Advance(200 * time.Millisecond) // r1 在 300ms 处成功
	o := h.Wait()
	if o.Kind != Success || o.Value != "win-r1" {
		t.Fatalf("结局不符: %+v", o)
	}

	env.clock.Advance(100 * time.Millisecond) // r0 在 400ms 处迟到
	if o.Value != "win-r1" {
		t.Fatalf("迟到结果不应改变结局: %+v", o)
	}
	assertLogContains(t, env, "late response discarded")
}

// 恰在截止时刻到达的成功视为超时；提前一刻到达则正常采用。
func TestSuccessExactlyAtDeadlineIsTimeout(t *testing.T) {
	t.Run("同刻成功视为超时", func(t *testing.T) {
		env := newEnv(t, 0, 0, map[string]script{
			"r0": after(1*time.Second, okResp("too-late")),
		})
		spec := baseSpec(env, "r0")
		spec.Deadline = env.start.Add(1 * time.Second)
		h := env.executor.Start(spec)

		env.clock.AdvanceTo(env.start.Add(1 * time.Second))
		o := h.Wait()
		if o.Kind != Timeout || !errors.Is(o.Reason, ErrTimeout) {
			t.Fatalf("恰在截止时刻的成功应判超时: %+v", o)
		}
		assertLogContains(t, env, "at/past deadline discarded")
	})

	t.Run("提前一刻成功被采用", func(t *testing.T) {
		env := newEnv(t, 0, 0, map[string]script{
			"r0": after(999*time.Millisecond, okResp("in-time")),
		})
		spec := baseSpec(env, "r0")
		spec.Deadline = env.start.Add(1 * time.Second)
		h := env.executor.Start(spec)

		env.clock.AdvanceTo(env.start.Add(1 * time.Second))
		o := h.Wait()
		if o.Kind != Success || o.Value != "in-time" {
			t.Fatalf("截止前的成功应被采用: %+v", o)
		}
	})
}
