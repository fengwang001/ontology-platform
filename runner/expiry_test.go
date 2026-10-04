package runner_test

import (
	"errors"
	"testing"

	"ontology/flow"
	"ontology/grants"
	"ontology/runner"
)

// suspend 制造一个 Deny 挂起的实例，返回到期时刻。
func suspend(t *testing.T, r *runner.Runner, inst string, now int64) int64 {
	t.Helper()
	out, err := r.StartStep(inst, now)
	if err != nil || out.Allowed {
		t.Fatalf("want Deny, got %+v err=%v", out, err)
	}
	st, err := r.Status(inst, now)
	if err != nil || st.State != runner.StateSuspended {
		t.Fatalf("want Suspended, got %+v err=%v", st, err)
	}
	return st.Deadline
}

// TestExpiryBoundary 验证到期恰等与差 1，以及 Status 的只读虚拟视图。
func TestExpiryBoundary(t *testing.T) {
	g, r := newEnv(t, 0b0001, 0b0011, []uint64{0b0010}, 10)
	if err := r.Launch("i", "d", "p", 0); err != nil {
		t.Fatalf("launch: %v", err)
	}
	if err := g.Revoke("p", 0b0010); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	deadline := suspend(t, r, "i", 5) // 到期时刻 15
	if deadline != 15 {
		t.Fatalf("deadline: want 15, got %d", deadline)
	}
	st, err := r.Status("i", 14) // 差 1：未到期
	if err != nil || st.State != runner.StateSuspended || st.Miss != 0b0010 {
		t.Fatalf("now=14: want Suspended miss=0b0010, got %+v err=%v", st, err)
	}
	st, err = r.Status("i", 15) // 恰等：虚拟到期
	t.Logf("Status now=15: %+v (恰等即到期)", st)
	if err != nil || st.State != runner.StateFailed || st.Term != runner.TermExpired || st.TermAt != 15 {
		t.Fatalf("now=15: want Failed(Expired)@15, got %+v err=%v", st, err)
	}
	if evs := auditOf(t, r, "i"); len(evs) != 1 { // Status 只读，不落 Expire
		t.Fatalf("status must be read-only, audit=%+v", evs)
	}
	if _, err := r.FinishStep("i", 14); !errors.Is(err, runner.ErrState) {
		t.Fatalf("finish on suspended: want ErrState, got %v", err)
	}
	if err := r.Approve("i", "a", 15); !errors.Is(err, runner.ErrState) {
		t.Fatalf("approve at deadline: want ErrState, got %v", err)
	}
	evs := auditOf(t, r, "i") // 到期被落实：恰一条 Expire
	if len(evs) != 2 || evs[1].Kind != runner.EvExpire || evs[1].At != 15 {
		t.Fatalf("want Expire@15, got %+v", evs)
	}
	st, _ = r.Status("i", 15)
	if st.State != runner.StateFailed || st.TermAt != 15 {
		t.Fatalf("materialized: want Failed@15, got %+v", st)
	}
}

// TestApproveBeforeDeadline 验证差 1 时 Approve 仍可放行。
func TestApproveBeforeDeadline(t *testing.T) {
	g, r := newEnv(t, 0b0001, 0b0011, []uint64{0b0010}, 10)
	if err := r.Launch("i", "d", "p", 0); err != nil {
		t.Fatalf("launch: %v", err)
	}
	if err := g.Revoke("p", 0b0010); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	suspend(t, r, "i", 5)
	if err := g.Grant("a", grants.ApproveBit); err != nil {
		t.Fatalf("grant approver: %v", err)
	}
	if err := r.Approve("i", "a", 14); err != nil { // 到期前 1：放行成功
		t.Fatalf("approve at deadline-1: %v", err)
	}
	if _, err := r.FinishStep("i", 15); err != nil {
		t.Fatalf("finish: %v", err)
	}
	st, _ := r.Status("i", 15)
	if st.State != runner.StateCompleted {
		t.Fatalf("want Completed, got %+v", st)
	}
}

// TestOverrideSingleStep 验证 Override 只覆盖当前一步。
func TestOverrideSingleStep(t *testing.T) {
	g, r := newEnv(t, 0b0001, 0b0111, []uint64{0b0010, 0b0100}, 10)
	if err := r.Launch("i", "d", "p", 0); err != nil {
		t.Fatalf("launch: %v", err)
	}
	suspend(t, r, "i", 1)
	if err := g.Grant("a", grants.ApproveBit); err != nil {
		t.Fatalf("grant approver: %v", err)
	}
	if err := r.Approve("i", "a", 2); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := r.FinishStep("i", 3); err != nil {
		t.Fatalf("finish: %v", err)
	}
	out, err := r.StartStep("i", 4) // 越权不延续到下一步
	t.Logf("override 后 StartStep: %+v err=%v", out, err)
	if err != nil || out.Allowed || out.Miss != 0b0100 {
		t.Fatalf("want Deny miss=0b0100, got %+v err=%v", out, err)
	}
}

// TestSelfBeforeNoAuthority 验证自批判定先于无权判定。
func TestSelfBeforeNoAuthority(t *testing.T) {
	g, r := newEnv(t, 0b0001, 0b0011, []uint64{0b0010}, 10)
	if err := r.Launch("i", "d", "p", 0); err != nil {
		t.Fatalf("launch: %v", err)
	}
	suspend(t, r, "i", 1)
	if err := r.Approve("i", "p", 2); !errors.Is(err, runner.ErrSelf) { // p 无位 63，仍报 ErrSelf
		t.Fatalf("self without bit63: want ErrSelf, got %v", err)
	}
	if err := g.Grant("p", grants.ApproveBit); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if err := r.Approve("i", "p", 2); !errors.Is(err, runner.ErrSelf) { // p 有位 63，仍报 ErrSelf
		t.Fatalf("self with bit63: want ErrSelf, got %v", err)
	}
	if err := r.Reject("i", "p", 2); !errors.Is(err, runner.ErrSelf) {
		t.Fatalf("self reject: want ErrSelf, got %v", err)
	}
}

// TestDefineValidation 验证定义校验：位 63 不可进 ceil 等。
func TestDefineValidation(t *testing.T) {
	f := flow.New()
	cases := []struct {
		name string
		ceil uint64
		reqs []uint64
	}{
		{"", 0b1, []uint64{0b1}},
		{"d", grants.ApproveBit | 0b1, []uint64{0b1}}, // 位 63 不可进 ceil
		{"d", 0b1, nil},
		{"d", 0b1, make([]uint64, 17)},
		{"d", 0b1, []uint64{0}},
		{"d", 0b1, []uint64{0b10}}, // req 不是 ceil 子集
	}
	for _, c := range cases {
		if err := f.Define(c.name, c.ceil, c.reqs); !errors.Is(err, flow.ErrDef) {
			t.Fatalf("define %+v: want ErrDef, got %v", c, err)
		}
	}
	if err := f.Define("d", 0b1, []uint64{0b1}); err != nil {
		t.Fatalf("valid define: %v", err)
	}
}

// TestParamAndClock 验证参数/时钟优先级与被拒绝操作不推进时钟。
func TestParamAndClock(t *testing.T) {
	g, r := newEnv(t, 0b0001, 0b0001, []uint64{0b0001}, 10)
	if err := r.Launch("", "d", "p", 0); !errors.Is(err, runner.ErrParam) {
		t.Fatalf("empty inst: want ErrParam, got %v", err)
	}
	if err := r.Launch("i", "d", "p", runner.MaxNow+1); !errors.Is(err, runner.ErrParam) {
		t.Fatalf("now out of range: want ErrParam, got %v", err)
	}
	if err := r.Launch("i", "d", "p", 10); err != nil {
		t.Fatalf("launch: %v", err)
	}
	if err := r.Launch("j", "d", "p", 9); !errors.Is(err, runner.ErrClock) {
		t.Fatalf("clock backwards: want ErrClock, got %v", err)
	}
	if err := r.Approve("i", "a", 50); !errors.Is(err, runner.ErrState) { // 被拒绝，不推进时钟
		t.Fatalf("approve on active: want ErrState, got %v", err)
	}
	if _, err := r.FinishStep("i", 10); !errors.Is(err, runner.ErrState) {
		t.Fatalf("finish without running: want ErrState, got %v", err)
	}
	if _, err := r.StartStep("i", 10); err != nil {
		t.Fatalf("clock must not advance on rejection: %v", err)
	}
	st, err := r.Status("i", 99) // Status 只读，不推进时钟
	if err != nil || st.State != runner.StateActive || st.Steps[0] != runner.StepRunning {
		t.Fatalf("status: %+v err=%v", st, err)
	}
	if _, err := r.FinishStep("i", 10); err != nil {
		t.Fatalf("finish running step: %v", err)
	}
	if err := g.Grant("", 0b1); !errors.Is(err, grants.ErrParam) {
		t.Fatalf("grant empty principal: want ErrParam, got %v", err)
	}
	if err := g.Revoke("p", 0); !errors.Is(err, grants.ErrParam) {
		t.Fatalf("revoke zero mask: want ErrParam, got %v", err)
	}
}
