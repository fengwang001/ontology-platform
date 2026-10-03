package runner_test

import (
	"testing"

	"ontology/flow"
	"ontology/grants"
	"ontology/runner"
)

// setupDenied 构造「步0 在 StartStep@1 被 Deny、deadline=1+T」的实例 i：
// p 拥有 0b0111，ceil=0b0110，Launch@0 后撤销位 1，req0=0b0010 缺失。
func setupDenied(t *testing.T, r *runner.Runner, g *grants.Registry, c *flow.Catalog) {
	t.Helper()
	if err := g.Grant([]byte("p"), 0b0111); err != nil {
		t.Fatal(err)
	}
	if err := c.Define([]byte("d"), 0b0110, []uint64{0b0010, 0b0100}); err != nil {
		t.Fatal(err)
	}
	if err := r.Launch([]byte("i"), []byte("d"), []byte("p"), 0); err != nil {
		t.Fatal(err)
	}
	if err := g.Revoke([]byte("p"), 0b0010); err != nil {
		t.Fatal(err)
	}
	if err := r.StartStep([]byte("i"), 1); err != nil {
		t.Fatal(err)
	}
	st, err := r.Status([]byte("i"), 1)
	if err != nil || !st.Suspended {
		t.Fatalf("setupDenied 前置失败: %+v err=%v", st, err)
	}
}
