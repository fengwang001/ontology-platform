package runner_test

import (
	"errors"
	"testing"

	"ontology/runner"
)

func TestArgumentPriority(t *testing.T) {
	r, g, c := newKit(t, 10)
	// 空 inst 优先于一切（即便时钟也不合法）。
	requireErrIs(t, r.Launch(nil, []byte("d"), nil, -1), runner.ErrArg, "空inst+空p+负now")
	requireErrIs(t, r.StartStep(nil, -1), runner.ErrArg, "空inst+负now")
	requireErrIs(t, r.FinishStep(nil, -1), runner.ErrArg, "Finish空inst")
	requireErrIs(t, r.Approve(nil, nil, -1), runner.ErrArg, "Approve空inst空a")
	requireErrIs(t, r.Reject(nil, nil, 0), runner.ErrArg, "Reject空inst空a")
	_, err := r.Status(nil, 0)
	requireErrIs(t, err, runner.ErrArg, "Status空inst")
	_, err = r.AuditLog(nil)
	requireErrIs(t, err, runner.ErrArg, "AuditLog空inst")
	// T 越界。
	if _, err := runner.New(g, c, 0); !errors.Is(err, runner.ErrArg) {
		t.Fatalf("T=0: %v", err)
	}
	if _, err := runner.New(g, c, 1_000_000_001); !errors.Is(err, runner.ErrArg) {
		t.Fatalf("T=1e9+1: %v", err)
	}
	if _, err := runner.New(nil, nil, 1); !errors.Is(err, runner.ErrArg) {
		t.Fatalf("nil 依赖: %v", err)
	}
	// Launch：实例已存在先于定义不存在。
	if err := c.Define([]byte("d"), 1, []uint64{1}); err != nil {
		t.Fatal(err)
	}
	if err := r.Launch([]byte("i"), []byte("d"), []byte("p"), 0); err != nil {
		t.Fatal(err)
	}
	requireErrIs(t, r.Launch([]byte("i"), []byte("missing"), []byte("p"), 0),
		runner.ErrExists, "实例已存在先于定义不存在")
	// 定义不存在。
	requireErrIs(t, r.Launch([]byte("i2"), []byte("missing"), []byte("p"), 0),
		runner.ErrNotFound, "定义不存在")
}

func TestClockRules(t *testing.T) {
	r, g, c := newKit(t, 10)
	if err := g.Grant([]byte("p"), 1); err != nil {
		t.Fatal(err)
	}
	if err := c.Define([]byte("d"), 1, []uint64{1}); err != nil {
		t.Fatal(err)
	}
	requireErrIs(t, r.Launch([]byte("i"), []byte("d"), []byte("p"), -1), runner.ErrClock, "now<0")
	requireErrIs(t, r.Launch([]byte("i"), []byte("d"), []byte("p"), 1_000_000_000_000_001),
		runner.ErrClock, "now>1e15")
	if err := r.Launch([]byte("i"), []byte("d"), []byte("p"), 5); err != nil {
		t.Fatal(err)
	}
	requireErrIs(t, r.StartStep([]byte("i"), 4), runner.ErrClock, "时钟倒退")
	if err := r.StartStep([]byte("i"), 5); err != nil {
		t.Fatal(err) // 同时刻合法
	}
	requireErrIs(t, r.StartStep([]byte("i"), 5), runner.ErrState, "Running 时再 StartStep")
	if err := r.FinishStep([]byte("i"), 5); err != nil {
		t.Fatal(err)
	}
	// 全部 Done：StartStep/FinishStep 均 ErrState。
	requireErrIs(t, r.StartStep([]byte("i"), 6), runner.ErrState, "Completed 后 StartStep")
	requireErrIs(t, r.FinishStep([]byte("i"), 6), runner.ErrState, "无 Running 时 FinishStep")
	_, err := r.Status([]byte("i"), 4)
	requireErrIs(t, err, runner.ErrClock, "Status 也不得早于全局时钟")
	if _, err := r.Status([]byte("i"), 5); err != nil {
		t.Fatalf("Status@5（等于时钟）应只读成功: %v", err)
	}
	// 被拒绝操作不推进时钟：now=6 的拒绝后，now=5 仍合法。
	if _, err := r.Status([]byte("i"), 6); err != nil {
		t.Fatal(err)
	}
	// 实例不存在。
	requireErrIs(t, r.StartStep([]byte("zzz"), 7), runner.ErrNotFound, "实例不存在")
	requireErrIs(t, r.Approve([]byte("zzz"), []byte("a"), 7), runner.ErrNotFound, "Approve 不存在")
}

func TestSuspendedStateRules(t *testing.T) {
	r, g, c := newKit(t, 100)
	setupDenied(t, r, g, c)
	// Suspended 时 FinishStep / 重复 Approve 前置错误。
	requireErrIs(t, r.FinishStep([]byte("i"), 2), runner.ErrState, "Suspended 时 FinishStep")
	// 非 Suspended 实例上 Approve。
	if err := g.Grant([]byte("p"), 0b1111); err != nil { // 含位0..3
		t.Fatal(err)
	}
	if err := c.Define([]byte("d2"), 1, []uint64{1}); err != nil {
		t.Fatal(err)
	}
	if err := r.Launch([]byte("j"), []byte("d2"), []byte("p"), 2); err != nil {
		t.Fatal(err)
	}
	requireErrIs(t, r.Approve([]byte("j"), []byte("a"), 3), runner.ErrState, "非 Suspended Approve")
	requireErrIs(t, r.Reject([]byte("j"), []byte("p"), 3), runner.ErrState, "非 Suspended Reject 先于 ErrSelf")
}
