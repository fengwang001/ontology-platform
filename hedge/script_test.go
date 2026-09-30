package hedge

import (
	"bytes"
	"log"
	"sync"
	"testing"
	"time"
)

// script 描述一个副本的应答脚本：respondAt 为 nil 表示永不响应。
type script struct {
	respondAfter *time.Duration // 自请求发出起多久后投递应答
	resp         Response
}

func after(d time.Duration, resp Response) script {
	return script{respondAfter: &d, resp: resp}
}

func never() script { return script{} }

type sendEvent struct {
	replica string
	at      time.Time
}

// scriptedTransport 按脚本应答的 Transport：取消后仍可能迟到投递，
// 用于验证执行器丢弃迟到结果。全部事件经由 ManualClock 同步驱动，可重放。
type scriptedTransport struct {
	clock   *ManualClock
	mu      sync.Mutex
	scripts map[string]script
	sends   []sendEvent
	cancels []string
}

func newScriptedTransport(clock *ManualClock, scripts map[string]script) *scriptedTransport {
	return &scriptedTransport{clock: clock, scripts: scripts}
}

func (t *scriptedTransport) Start(replica string, deliver func(Response)) func() {
	t.mu.Lock()
	t.sends = append(t.sends, sendEvent{replica: replica, at: t.clock.Now()})
	sc, ok := t.scripts[replica]
	t.mu.Unlock()
	if ok && sc.respondAfter != nil {
		t.clock.AfterFunc(*sc.respondAfter, func() { deliver(sc.resp) })
	}
	return func() {
		t.mu.Lock()
		t.cancels = append(t.cancels, replica)
		t.mu.Unlock()
	}
}

func (t *scriptedTransport) sendLog() []sendEvent {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]sendEvent(nil), t.sends...)
}

func (t *scriptedTransport) cancelLog() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.cancels...)
}

// testEnv 一次测试场景：手动时钟 + 预算 + 脚本传输 + 带日志的执行器。
type testEnv struct {
	clock     *ManualClock
	budget    *Budget
	transport *scriptedTransport
	executor  *Executor
	start     time.Time
	logs      *bytes.Buffer
}

func newEnv(t *testing.T, fixed int, ratio float64, scripts map[string]script) *testEnv {
	t.Helper()
	start := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	clock := NewManualClock(start)
	budget := NewBudget(fixed, ratio)
	transport := newScriptedTransport(clock, scripts)
	logs := &bytes.Buffer{}
	executor := NewExecutor(clock, budget, transport, log.New(logs, "", 0))
	env := &testEnv{
		clock: clock, budget: budget, transport: transport,
		executor: executor, start: start, logs: logs,
	}
	t.Cleanup(func() {
		t.Logf("执行器日志（输入/输出/判定依据）:\n%s", logs.String())
	})
	return env
}
