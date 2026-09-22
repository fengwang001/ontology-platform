package gateway

import (
	"bytes"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func newTestGateway(ttl time.Duration) (*Gateway, *fakeClock) {
	clock := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	return New(Config{Now: func() time.Time { return clock.t }, TTL: ttl}), clock
}

func countingExec(g *Gateway, result []byte, err error) ExecFunc {
	return func(body []byte) ([]byte, error) {
		return result, err
	}
}

func TestExecuteOnceAndReplay(t *testing.T) {
	g, _ := newTestGateway(time.Minute)
	exec := func([]byte) ([]byte, error) { return []byte("v1"), nil }

	first := g.Submit("k", []byte("body"), exec)
	if first.Replayed || first.Err != nil || !bytes.Equal(first.Result, []byte("v1")) {
		t.Fatalf("首次提交应为真实执行: %+v", first)
	}

	for i := 0; i < 5; i++ {
		out := g.Submit("k", []byte("body"), exec)
		if !out.Replayed || !bytes.Equal(out.Result, first.Result) {
			t.Fatalf("第 %d 次重复必须回放且结果等价: %+v", i+2, out)
		}
	}
	if g.ExecCalls() != 1 {
		t.Fatalf("执行函数应只被调用一次，实际 %d", g.ExecCalls())
	}
}

func TestConflictDifferentBodyDoesNotExecute(t *testing.T) {
	g, _ := newTestGateway(time.Minute)
	g.Submit("k", []byte("body-a"), func([]byte) ([]byte, error) { return []byte("A"), nil })

	out := g.Submit("k", []byte("body-b"), func([]byte) ([]byte, error) {
		t.Fatal("冲突请求不得触发执行")
		return nil, nil
	})
	if !IsConflict(out.Err) {
		t.Fatalf("不同请求体必须报冲突，实际 %v", out.Err)
	}

	replay := g.Submit("k", []byte("body-a"), nil)
	if replay.Replayed != true || !bytes.Equal(replay.Result, []byte("A")) {
		t.Fatal("冲突不得覆盖已保存的原结果")
	}
	if g.ExecCalls() != 1 {
		t.Fatalf("冲突后执行次数仍应为 1，实际 %d", g.ExecCalls())
	}
}

func TestFailureRetryThenFixed(t *testing.T) {
	g, _ := newTestGateway(time.Minute)
	var calls atomic.Int64
	shouldFail := true
	exec := func([]byte) ([]byte, error) {
		calls.Add(1)
		if shouldFail {
			return nil, errors.New("transient")
		}
		return []byte("done"), nil
	}

	f1 := g.Submit("k", []byte("b"), exec)
	if f1.Replayed || f1.Err == nil || f1.Err.Error() != "transient" {
		t.Fatal("失败必须原样返回错误且标记为本次执行")
	}

	shouldFail = false
	f2 := g.Submit("k", []byte("b"), exec)
	if f2.Replayed || !bytes.Equal(f2.Result, []byte("done")) {
		t.Fatal("失败后同键同体重试应真正再执行并成功")
	}

	f3 := g.Submit("k", []byte("b"), exec)
	if !f3.Replayed || !bytes.Equal(f3.Result, []byte("done")) {
		t.Fatal("成功后必须固定，只回放成功那一次")
	}
	if calls.Load() != 2 || g.ExecCalls() != 2 {
		t.Fatalf("执行计数应为 2，实际 calls=%d gateway=%d", calls.Load(), g.ExecCalls())
	}

	// 成功后换体仍然冲突，且不执行。
	out := g.Submit("k", []byte("other"), exec)
	if !IsConflict(out.Err) || calls.Load() != 2 {
		t.Fatal("成功后换体必须冲突且不执行")
	}
}

func TestExpiryBoundaryAndRerun(t *testing.T) {
	g, clock := newTestGateway(time.Minute)
	g.Submit("k", []byte("b"), countingExec(g, []byte("old"), nil))

	clock.t = clock.t.Add(time.Minute - time.Nanosecond)
	if info := g.Lookup("k"); !info.Known {
		t.Fatal("过期前一刻键仍应存在")
	}

	clock.t = clock.t.Add(time.Nanosecond) // 恰好等于过期时刻
	if info := g.Lookup("k"); info.Known {
		t.Fatal("恰好到期必须视为不存在，返回零值")
	}

	g.Submit("k", []byte("b"), countingExec(g, []byte("new"), nil))
	if g.ExecCalls() != 2 {
		t.Fatal("过期后再提交必须真正再执行一次")
	}
	if info := g.Lookup("k"); !info.Known || !info.HasResult {
		t.Fatal("重新执行后应得到新的在期记录")
	}
}

func TestLookupUnknownReturnsZero(t *testing.T) {
	g, _ := newTestGateway(time.Minute)
	if info := g.Lookup("missing"); info != (Info{}) {
		t.Fatalf("不存在的键必须返回零值，实际 %+v", info)
	}
	g.Submit("k", []byte("b"), func([]byte) ([]byte, error) { return nil, errors.New("x") })
	info := g.Lookup("k")
	if !info.Known || info.State == 0 || info.HasResult {
		t.Fatalf("失败记录应已知、无结果，实际 %+v", info)
	}
}
