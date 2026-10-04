package runner_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/flow"
	"ontology/grants"
	"ontology/runner"
)

func newEnv(t *testing.T, mask, ceil uint64, reqs []uint64, timeout int64) (*grants.Registry, *runner.Runner) {
	t.Helper()
	g := grants.New()
	f := flow.New()
	if err := g.Grant("p", mask); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if err := f.Define("d", ceil, reqs); err != nil {
		t.Fatalf("define: %v", err)
	}
	r, err := runner.New(g, f, timeout)
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}
	return g, r
}

func auditOf(t *testing.T, r *runner.Runner, inst string) []runner.Event {
	t.Helper()
	evs, err := r.Audit(inst)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	for i, e := range evs {
		if e.Seq != i+1 {
			t.Fatalf("audit seq gap: %+v", evs)
		}
	}
	return evs
}

// TestWorkedExample 逐步复现需求中的例子并核对审计序列。
func TestWorkedExample(t *testing.T) {
	g, r := newEnv(t, 0b0111, 0b0110, []uint64{0b0010, 0b0100}, 10)
	if err := r.Launch("i", "d", "p", 0); err != nil {
		t.Fatalf("launch: %v", err)
	}
	_ = g.Revoke("p", 0b0010)
	out, err := r.StartStep("i", 1)
	t.Logf("StartStep now=1 -> %+v err=%v (eff=0b0100, req=0b0010 不满足)", out, err)
	if err != nil || out.Allowed || out.Miss != 0b0010 {
		t.Fatalf("want Deny miss=0b0010, got %+v err=%v", out, err)
	}
	_ = g.Grant("p", 0b1000) // 快照 E 不含位 3，不生效
	if err := r.Approve("i", "p", 2); !errors.Is(err, runner.ErrSelf) {
		t.Fatalf("self approve: want ErrSelf, got %v", err)
	}
	if err := r.Approve("i", "a", 2); !errors.Is(err, runner.ErrNoAuthority) {
		t.Fatalf("no authority: want ErrNoAuthority, got %v", err)
	}
	_ = g.Grant("a", grants.ApproveBit)
	if err := r.Approve("i", "a", 3); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := r.FinishStep("i", 4); err != nil {
		t.Fatalf("finish: %v", err)
	}
	out, err = r.StartStep("i", 5)
	if err != nil || !out.Allowed || out.Index != 1 {
		t.Fatalf("step1: want Allow, got %+v err=%v", out, err)
	}
	if _, err := r.FinishStep("i", 6); err != nil {
		t.Fatalf("finish: %v", err)
	}
	st, _ := r.Status("i", 6)
	if st.State != runner.StateCompleted {
		t.Fatalf("want Completed, got %v", st.State)
	}
	evs := auditOf(t, r, "i")
	want := []runner.EvKind{runner.EvDeny, runner.EvOverride, runner.EvAllow}
	if len(evs) != len(want) {
		t.Fatalf("audit len: want %d, got %d (%+v)", len(want), len(evs), evs)
	}
	for i, k := range want {
		if evs[i].Kind != k {
			t.Fatalf("audit[%d]: want %v, got %+v", i, k, evs[i])
		}
	}
	t.Logf("audit: %+v", evs)
	if evs[0].Miss != 0b0010 || evs[1].Actor != "a" {
		t.Fatalf("audit detail: %+v", evs)
	}
}

// TestSnapshotSemantics 验证启动后 Grant 不生效、Revoke 立即生效。
func TestSnapshotSemantics(t *testing.T) {
	g, r := newEnv(t, 0b0011, 0b0111, []uint64{0b0100}, 10)
	if err := r.Launch("i1", "d", "p", 0); err != nil {
		t.Fatalf("launch: %v", err)
	}
	_ = g.Grant("p", 0b0100) // 启动后新增授权被快照封顶
	out, err := r.StartStep("i1", 1)
	t.Logf("grant 后 StartStep: %+v err=%v (eff 仍为 0b0011)", out, err)
	if err != nil || out.Allowed || out.Miss != 0b0100 {
		t.Fatalf("want Deny miss=0b0100, got %+v err=%v", out, err)
	}
	g2, r2 := newEnv(t, 0b0111, 0b0111, []uint64{0b0100}, 10)
	if err := r2.Launch("i2", "d", "p", 0); err != nil {
		t.Fatalf("launch: %v", err)
	}
	_ = g2.Revoke("p", 0b0100) // 撤销立即生效
	out, err = r2.StartStep("i2", 1)
	if err != nil || out.Allowed || out.Miss != 0b0100 {
		t.Fatalf("want Deny miss=0b0100, got %+v err=%v", out, err)
	}
}

// TestConcurrentInterleaving 在 -race 下交错 Revoke/Grant 与 StartStep/Approve，
// 结果等价于某个串行顺序：审计序号必须连续、终局唯一。
func TestConcurrentInterleaving(t *testing.T) {
	g := grants.New()
	f := flow.New()
	if err := f.Define("d", 0x7, []uint64{1, 2, 4}); err != nil {
		t.Fatalf("define: %v", err)
	}
	r, err := runner.New(g, f, 5)
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}
	_ = g.Grant("p", 0x7)
	_ = g.Grant("a", grants.ApproveBit)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			inst := fmt.Sprintf("i%d", w%4)
			for i := 0; i < 300; i++ {
				switch (w + i) % 6 {
				case 0:
					_ = r.Launch(inst, "d", "p", 0)
				case 1:
					_, _ = r.StartStep(inst, 0)
				case 2:
					_ = g.Revoke("p", uint64(1)<<uint(i%3))
				case 3:
					_ = g.Grant("p", uint64(1)<<uint(i%3))
				case 4:
					_, _ = r.FinishStep(inst, 0)
				case 5:
					_ = r.Approve(inst, "a", 0)
				}
			}
		}(w)
	}
	wg.Wait()
	for _, inst := range []string{"i0", "i1", "i2", "i3"} {
		evs, err := r.Audit(inst)
		if errors.Is(err, runner.ErrNotFound) {
			continue
		}
		if err != nil {
			t.Fatalf("audit %s: %v", inst, err)
		}
		for i, e := range evs {
			if e.Seq != i+1 {
				t.Fatalf("audit seq gap in %s: %+v", inst, evs)
			}
		}
		st, err := r.Status(inst, 0)
		if err != nil {
			t.Fatalf("status %s: %v", inst, err)
		}
		t.Logf("%s: state=%v steps=%v events=%d", inst, st.State, st.Steps, len(evs))
	}
}

// TestRevokeDuringRunning 验证 Running 步骤不被撤权打断，下一步才拦截。
func TestRevokeDuringRunning(t *testing.T) {
	g, r := newEnv(t, 0b0110, 0b0110, []uint64{0b0010, 0b0100}, 10)
	if err := r.Launch("i", "d", "p", 0); err != nil {
		t.Fatalf("launch: %v", err)
	}
	out, err := r.StartStep("i", 1)
	if err != nil || !out.Allowed {
		t.Fatalf("step0: want Allow, got %+v err=%v", out, err)
	}
	_ = g.Revoke("p", 0b0100)
	if _, err := r.FinishStep("i", 2); err != nil { // 在跑步骤照常完成
		t.Fatalf("finish during revoke: %v", err)
	}
	out, err = r.StartStep("i", 3)
	t.Logf("revoke 后 StartStep: %+v err=%v (eff=0b0010)", out, err)
	if err != nil || out.Allowed || out.Miss != 0b0100 {
		t.Fatalf("want Deny miss=0b0100, got %+v err=%v", out, err)
	}
}
