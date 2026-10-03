package runner_test

import (
	"errors"
	"testing"

	"ontology/flow"
	"ontology/grants"
)

// 快照封顶：Launch 后 Grant 不生效、Revoke 生效。
func TestSnapshotCap(t *testing.T) {
	r, g, c := newKit(t, 100)
	if err := g.Grant([]byte("p"), 0b0011); err != nil {
		t.Fatal(err)
	}
	if err := c.Define([]byte("d"), 0b0111, []uint64{0b0001, 0b0010, 0b0100}); err != nil {
		t.Fatal(err)
	}
	if err := r.Launch([]byte("i"), []byte("d"), []byte("p"), 0); err != nil {
		t.Fatal(err)
	}
	if err := g.Grant([]byte("p"), 0b1000); err != nil {
		t.Fatal(err) // 位 3 不在 ceil 0b0111
	}
	if err := g.Grant([]byte("p"), 0b0100); err != nil {
		t.Fatal(err) // 位 2 在 ceil，但 Launch 后才授予
	}
	if err := r.StartStep([]byte("i"), 1); err != nil {
		t.Fatal(err) // 步0 req=1：E=0b0011，实时=0b1111，eff=0b0011 → Allow
	}
	if err := r.FinishStep([]byte("i"), 2); err != nil {
		t.Fatal(err)
	}
	if err := r.StartStep([]byte("i"), 3); err != nil {
		t.Fatal(err) // 步1 req=2：eff=0b0011 → Allow
	}
	if err := r.FinishStep([]byte("i"), 4); err != nil {
		t.Fatal(err)
	}
	// 步2 req=4：新增的位2被 E 封顶 → Deny。
	if err := r.StartStep([]byte("i"), 5); err != nil {
		t.Fatal(err)
	}
	st, _ := r.Status([]byte("i"), 5)
	if !st.Suspended || st.Miss != 0b0100 {
		t.Fatalf("Launch 后 Grant 应被封顶: %+v", st)
	}
	t.Logf("步2 miss=%#b；判定依据 eff=实时&E，E=0b0011 不含位2", st.Miss)

	// Revoke 立即生效：恢复到挂起前状态后撤销位 0，剩余 Pending 步骤仍受影响。
	r2, g2, c2 := newKit(t, 100)
	if err := g2.Grant([]byte("p"), 0b0011); err != nil {
		t.Fatal(err)
	}
	if err := c2.Define([]byte("d"), 0b0111, []uint64{0b0001, 0b0010}); err != nil {
		t.Fatal(err)
	}
	if err := r2.Launch([]byte("j"), []byte("d"), []byte("p"), 0); err != nil {
		t.Fatal(err)
	}
	if err := g2.Revoke([]byte("p"), 0b0001); err != nil {
		t.Fatal(err)
	}
	if err := r2.StartStep([]byte("j"), 1); err != nil {
		t.Fatal(err)
	}
	st2, _ := r2.Status([]byte("j"), 1)
	if !st2.Suspended || st2.Miss != 0b0001 {
		t.Fatalf("Launch 后 Revoke 应立即生效: %+v", st2)
	}
	t.Logf("Revoke 后步0 miss=%#b；判定依据实时撤销立即进入 eff", st2.Miss)
}

// Running 期间撤权不打断当前步，下一步才拦截。
func TestRevokeDuringRunning(t *testing.T) {
	r, g, c := newKit(t, 100)
	if err := g.Grant([]byte("p"), 0b0111); err != nil {
		t.Fatal(err)
	}
	if err := c.Define([]byte("d"), 0b0111, []uint64{0b0001, 0b0100}); err != nil {
		t.Fatal(err)
	}
	if err := r.Launch([]byte("i"), []byte("d"), []byte("p"), 0); err != nil {
		t.Fatal(err)
	}
	if err := r.StartStep([]byte("i"), 1); err != nil {
		t.Fatal(err) // 步0 Running
	}
	if err := g.Revoke([]byte("p"), 0b0100); err != nil {
		t.Fatal(err)
	}
	if err := r.FinishStep([]byte("i"), 2); err != nil {
		t.Fatalf("Running 期间撤权不应打断 Finish: %v", err)
	}
	if err := r.StartStep([]byte("i"), 3); err != nil {
		t.Fatal(err)
	}
	st, _ := r.Status([]byte("i"), 3)
	if !st.Suspended || st.Miss != 0b0100 {
		t.Fatalf("撤权应在下一步拦截: %+v", st)
	}
	t.Logf("步0 照常 Done，步1 miss=%#b；判定依据决策只在步骤边界发生", st.Miss)
}

// Override 只管一步：放行后后续步骤仍按 eff 判定。
func TestOverrideOnlyOneStep(t *testing.T) {
	r, g, c := newKit(t, 100)
	if err := c.Define([]byte("d"), 0b0011, []uint64{0b0001, 0b0010}); err != nil {
		t.Fatal(err) // p 无任何权限
	}
	if err := g.Grant([]byte("a"), grants.ApproverBit); err != nil {
		t.Fatal(err)
	}
	if err := r.Launch([]byte("i"), []byte("d"), []byte("p"), 0); err != nil {
		t.Fatal(err)
	}
	if err := r.StartStep([]byte("i"), 1); err != nil {
		t.Fatal(err)
	}
	if err := r.Approve([]byte("i"), []byte("a"), 2); err != nil {
		t.Fatal(err) // 只越权步0
	}
	if err := r.FinishStep([]byte("i"), 3); err != nil {
		t.Fatal(err)
	}
	if err := r.StartStep([]byte("i"), 4); err != nil {
		t.Fatal(err) // 步1 仍 Deny，不继承越权
	}
	st, _ := r.Status([]byte("i"), 4)
	if !st.Suspended || st.Miss != 0b0010 {
		t.Fatalf("Override 不应覆盖后续步: %+v", st)
	}
	t.Logf("步1 miss=%#b；判定依据 Override 仅当前一步", st.Miss)
}

// 位 63 不可进 ceil。
func TestBit63NotInCeil(t *testing.T) {
	c := flow.NewCatalog()
	err := c.Define([]byte("d"), uint64(1)<<63, []uint64{uint64(1) << 63})
	if !errors.Is(err, flow.ErrDef) {
		t.Fatalf("ceil 含位63: err=%v, 期望 ErrDef", err)
	}
	t.Logf("Define(ceil 含位63) → %v；判定依据审批位不属于业务上限", err)
}
