package runner_test

import (
	"sync"
	"testing"

	"ontology/flow"
	"ontology/grants"
	"ontology/runner"
)

// countingMask 包装真实权限表并计数 Mask 读取次数。
type countingMask struct {
	inner *grants.Registry
	mu    sync.Mutex
	reads int
}

func (c *countingMask) Mask(p []byte) uint64 {
	c.mu.Lock()
	c.reads++
	c.mu.Unlock()
	return c.inner.Mask(p)
}

func (c *countingMask) Reads() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reads
}

// TestStartStepReadsGrantsOnce：一次 StartStep 对触发者掩码恰读一次，
// 无论 Allow 还是 Deny；Launch 与 Approve/Reject 的资格读取各自独立计数。
func TestStartStepReadsGrantsOnce(t *testing.T) {
	reg := grants.New()
	cm := &countingMask{inner: reg}
	c := flow.NewCatalog()
	r, err := runner.New(cm, c, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Grant([]byte("p"), 0b0011); err != nil {
		t.Fatal(err)
	}
	if err := c.Define([]byte("d"), 0b0011, []uint64{0b0001, 0b0010}); err != nil {
		t.Fatal(err)
	}
	if err := r.Launch([]byte("i"), []byte("d"), []byte("p"), 0); err != nil {
		t.Fatal(err)
	}
	if cm.Reads() != 1 {
		t.Fatalf("Launch 应读 1 次权限, 实际 %d", cm.Reads())
	}
	if err := r.StartStep([]byte("i"), 1); err != nil { // Allow
		t.Fatal(err)
	}
	if cm.Reads() != 2 {
		t.Fatalf("Allow 型 StartStep 后应读 2 次, 实际 %d", cm.Reads())
	}
	t.Logf("Launch+Allow StartStep 后 Mask 次数 = %d（各恰 1 次）", cm.Reads())
	if err := r.FinishStep([]byte("i"), 2); err != nil {
		t.Fatal(err)
	}
	if err := reg.Revoke([]byte("p"), 0b0010); err != nil {
		t.Fatal(err)
	}
	if err := r.StartStep([]byte("i"), 3); err != nil { // Deny
		t.Fatal(err)
	}
	if cm.Reads() != 3 {
		t.Fatalf("Deny 型 StartStep 后应读 3 次, 实际 %d", cm.Reads())
	}
	t.Logf("Deny 型 StartStep 后 Mask 次数 = %d（判定依据 StartStep 恰读一次）", cm.Reads())
	// 被拒绝的 StartStep（状态不符）不应产生读取：先放行并收尾到终局。
	if err := reg.Grant([]byte("a"), grants.ApproverBit); err != nil {
		t.Fatal(err)
	}
	if err := r.Approve([]byte("i"), []byte("a"), 4); err != nil {
		t.Fatal(err)
	}
	if err := r.FinishStep([]byte("i"), 5); err != nil {
		t.Fatal(err)
	}
	readsNow := cm.Reads()
	if err := r.StartStep([]byte("i"), 6); err == nil { // 已完成
		t.Fatal("终局后 StartStep 应 ErrState")
	}
	if cm.Reads() != readsNow+0 {
		t.Fatalf("被状态拒绝的 StartStep 不应读权限: %d→%d", readsNow, cm.Reads())
	}
	t.Logf("终局后 StartStep 被拒，Mask 次数保持 %d（拒绝操作不读权限）", cm.Reads())
}
