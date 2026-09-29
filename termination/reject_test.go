package termination

import (
	"errors"
	"strings"
	"testing"
)

// unchanged 执行被拒绝的操作后，验证任何状态都没有改变。
func unchanged(t *testing.T, d *Detector, before Snapshot, name string) {
	t.Helper()
	if after := d.Snapshot(); !snapEqual(before, after) {
		t.Fatalf("%s: rejected operation changed state:\nbefore=%+v\nafter =%+v",
			name, before, after)
	}
}

func TestInvalidConstruction(t *testing.T) {
	for _, n := range []int{0, -1, -7} {
		if _, err := New(n); !errors.Is(err, ErrInvalidN) {
			t.Fatalf("New(%d): %v", n, err)
		}
	}
}

func TestSendRejections(t *testing.T) {
	d := mustNew(t, 3)

	cases := []struct {
		name     string
		from, to int
		want     error
	}{
		{"to-high", 0, 9, ErrProcessOutOfRange},
		{"from-neg", -1, 1, ErrProcessOutOfRange},
		{"to-neg", 1, -2, ErrProcessOutOfRange},
		{"self", 1, 1, ErrSendToSelf},
	}
	for _, tc := range cases {
		before := d.Snapshot()
		_, err := d.Send(tc.from, tc.to)
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: want %v, got %v", tc.name, tc.want, err)
		}
		unchanged(t, d, before, tc.name)
	}

	if err := d.BecomeIdle(2); err != nil {
		t.Fatal(err)
	}
	before := d.Snapshot()
	if _, err := d.Send(2, 0); !errors.Is(err, ErrIdleSender) {
		t.Fatalf("idle-sender: %v", err)
	}
	unchanged(t, d, before, "idle-sender")
}

func TestBecomeIdleRejections(t *testing.T) {
	d := mustNew(t, 2)
	for _, p := range []int{-1, 5} {
		before := d.Snapshot()
		if err := d.BecomeIdle(p); !errors.Is(err, ErrProcessOutOfRange) {
			t.Fatalf("BecomeIdle(%d): %v", p, err)
		}
		unchanged(t, d, before, "idle-range")
	}
	if err := d.BecomeIdle(1); err != nil {
		t.Fatal(err)
	}
	before := d.Snapshot()
	if err := d.BecomeIdle(1); !errors.Is(err, ErrAlreadyIdle) {
		t.Fatalf("already-idle: %v", err)
	}
	unchanged(t, d, before, "already-idle")
}

func TestDeliverRejection(t *testing.T) {
	d := mustNew(t, 2)
	before := d.Snapshot()
	if _, err := d.Deliver(42); !errors.Is(err, ErrMessageNotFound) {
		t.Fatalf("missing message: %v", err)
	}
	unchanged(t, d, before, "missing-message")
}

func TestPassTokenRejections(t *testing.T) {
	d := mustNew(t, 3)

	// 越界优先于持有者、活跃性判定。
	before := d.Snapshot()
	if _, _, err := d.PassToken(9); !errors.Is(err, ErrProcessOutOfRange) {
		t.Fatalf("pass-range: %v", err)
	}
	unchanged(t, d, before, "pass-range")

	// 全员初始活跃：0 是持有者但活跃 -> ErrActiveHolder；
	// 1、2 非持有者 -> ErrNoToken（即使它们活跃）。
	if _, _, err := d.PassToken(0); !errors.Is(err, ErrActiveHolder) {
		t.Fatalf("active-holder-0: %v", err)
	}
	if _, _, err := d.PassToken(1); !errors.Is(err, ErrNoToken) {
		t.Fatalf("non-holder-active: %v", err)
	}

	// 发起第 1 轮后令牌在 2；空闲的 0 仍非持有者 -> ErrNoToken。
	for _, p := range []int{0, 1, 2} {
		if err := d.BecomeIdle(p); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := d.PassToken(0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.PassToken(0); !errors.Is(err, ErrNoToken) {
		t.Fatalf("non-holder-idle: %v", err)
	}

	// 令牌传给 1 后，给持有者 1 投递一条消息使其重新活跃 -> ErrActiveHolder。
	if _, _, err := d.PassToken(2); err != nil {
		t.Fatal(err)
	}
	// 用一条新消息唤醒 0，再由活跃的 0 发给持有者 1。
	// （2 已变白且空闲；先让 2 发消息给 0。）
	// 简化：直接让 1 自己无法发自己，所以借助仍活跃过的 2 已不行——
	// 改为重启一个全新检测器专门构造该状态。
	d2 := activeHolderFixture(t)
	if _, _, err := d2.PassToken(1); !errors.Is(err, ErrActiveHolder) {
		t.Fatalf("active-holder: %v", err)
	}
}

// activeHolderFixture 构造“持有者 1 活跃”的状态：
// 第 1 轮令牌已传到 1，随后一条发给 1 的消息到达使其活跃。
func activeHolderFixture(t *testing.T) *Detector {
	t.Helper()
	d := mustNew(t, 3)
	// 1 先给 0 发消息（此时 1 活跃），暂不投递。
	pending, err := d.Send(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []int{0, 1, 2} {
		if err := d.BecomeIdle(p); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := d.PassToken(0); err != nil { // 发起第 1 轮 -> 2
		t.Fatal(err)
	}
	if _, _, err := d.PassToken(2); err != nil { // -> 1（1 空闲）
		t.Fatal(err)
	}
	// 0 收消息后活跃变黑，再由活跃的 0 发消息给持有者 1。
	if _, err := d.Deliver(pending); err != nil {
		t.Fatal(err)
	}
	back, err := d.Send(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Deliver(back); err != nil {
		t.Fatal(err)
	}
	return d
}

// TestPostTerminationRejects 宣告后所有变更操作一律拒绝，Done 关闭。
func TestPostTerminationRejects(t *testing.T) {
	d := mustNew(t, 1)
	if err := d.BecomeIdle(0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.PassToken(0); err != nil {
		t.Fatal(err)
	}
	ann, r, err := d.PassToken(0)
	if err != nil || !ann || r != 1 {
		t.Fatalf("termination: ann=%v round=%d err=%v", ann, r, err)
	}
	select {
	case <-d.Done():
	default:
		t.Fatal("Done channel not closed after announcement")
	}
	if _, err := d.Send(0, 0); !errors.Is(err, ErrTerminated) {
		t.Fatalf("Send after term: %v", err)
	}
	if _, err := d.Deliver(1); !errors.Is(err, ErrTerminated) {
		t.Fatalf("Deliver after term: %v", err)
	}
	if err := d.BecomeIdle(0); !errors.Is(err, ErrTerminated) {
		t.Fatalf("BecomeIdle after term: %v", err)
	}
	if _, _, err := d.PassToken(0); !errors.Is(err, ErrTerminated) {
		t.Fatalf("PassToken after term: %v", err)
	}
}

// TestLoggerShowsInputsOutputsDecision 日志包含输入、输出与判定依据。
func TestLoggerShowsInputsOutputsDecision(t *testing.T) {
	log := &StringLogger{}
	d, err := New(2, WithLogger(log))
	if err != nil {
		t.Fatal(err)
	}
	id, err := d.Send(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Deliver(id); err != nil { // 1 活跃变黑
		t.Fatal(err)
	}
	if err := d.BecomeIdle(1); err != nil {
		t.Fatal(err)
	}
	if err := d.BecomeIdle(0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.PassToken(0); err != nil { // 第 1 轮 -> 1
		t.Fatal(err)
	}
	if _, _, err := d.PassToken(1); err != nil { // 1 黑 -> 令牌黑
		t.Fatal(err)
	}
	if ann, _, err := d.PassToken(0); err != nil || ann { // 黑令牌阻止第 1 轮
		t.Fatalf("round 1 must be blocked: ann=%v err=%v", ann, err)
	}
	if _, _, err := d.PassToken(1); err != nil { // 第 2 轮
		t.Fatal(err)
	}
	if ann, r, err := d.PassToken(0); err != nil || !ann || r != 2 {
		t.Fatalf("announcement: ann=%v round=%d err=%v", ann, r, err)
	}

	out := log.String()
	t.Log("\n" + out)
	for _, want := range []string{
		"input Send", "output msg#",
		"input Deliver", "color=Black",
		"input BecomeIdle",
		"input PassToken", "round 1 started",
		"judge:", "decision not terminated",
		"decision TERMINATED at round 2",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("log missing %q", want)
		}
	}
}
