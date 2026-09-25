package api_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
	"ontology/route"
)

func scenarioRebalanced(t *testing.T) *api.API {
	t.Helper()
	a, err := api.New(2)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, k := range []string{"a", "b", "c", "d", "e"} {
		if err := a.Put(k, string(k[0]-32)); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	if err := a.Rebalance(3); err != nil {
		t.Fatalf("Rebalance: %v", err)
	}
	return a
}

// TestScenarioABC 钉住第三节 (甲)(乙)(丙) 推导出的正确终态。
func TestScenarioABC(t *testing.T) {
	a := scenarioRebalanced(t)
	want := map[string]string{"a": "A", "b": "B", "c": "C", "d": "D", "e": "E"}
	for k, w := range want {
		if v, ok, err := a.Get(k); err != nil || !ok || v != w {
			t.Errorf("Get(%q)=%q,%v,%v want %q", k, v, ok, err, w)
		}
	}
	// (乙) 忘记删旧分区会让 b 残留在分区 0；正确实现下分区 0 只有 c。
	if _, ok := a.Dump()[0]["b"]; ok {
		t.Errorf("(乙) b leaked in partition 0; dump=%v", a.Dump())
	}
	// (丙) 旧路由 %2 向分区 0 问 b：必须回 movedTo=2，而非 not found。
	if _, _, moved, home, err := a.GetPartition(0, "b"); err != nil || !moved || home != 2 {
		t.Errorf("(丙) GetPartition(0,b) moved=%v home=%d err=%v want moved,2,nil", moved, home, err)
	}
}

// TestGetPartitionRedirection 钉住不变量 3：p≠home 恒回 moved（与存在性无关）。
func TestGetPartitionRedirection(t *testing.T) {
	a := scenarioRebalanced(t)
	cases := []struct {
		name                 string
		p                    int
		key                  string
		wantFound, wantMoved bool
		wantHome             int
	}{
		{"b-at-home", 2, "b", true, false, 2},
		{"b-stale-p0", 0, "b", false, true, 2},
		{"c-stale-old", 1, "c", false, true, 0},
		{"missing-stale", 0, "zz", false, true, route.Home("zz", 3)},
		{"missing-home", route.Home("zz", 3), "zz", false, false, route.Home("zz", 3)},
	}
	for _, c := range cases {
		v, found, moved, home, err := a.GetPartition(c.p, c.key)
		if err != nil || found != c.wantFound || moved != c.wantMoved || home != c.wantHome {
			t.Errorf("%s: v=%q found=%v moved=%v home=%d err=%v", c.name, v, found, moved, home, err)
		}
		if c.wantMoved && (found || v != "") {
			t.Errorf("%s: redirect must not report existence", c.name)
		}
	}
}

// TestRejectedOpsLeaveNoTrace 钉住不变量 4：三类错误互不相同、拒绝不留痕、之后仍可用。
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	a := scenarioRebalanced(t)
	before := a.Dump()
	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"New(0)", func() error { _, e := api.New(0); return e }, api.ErrInvalidN},
		{"New(-3)", func() error { _, e := api.New(-3); return e }, api.ErrInvalidN},
		{"Rebalance(0)", func() error { return a.Rebalance(0) }, api.ErrInvalidN},
		{"Rebalance(-1)", func() error { return a.Rebalance(-1) }, api.ErrInvalidN},
		{"GetPartition(-1)", func() error { _, _, _, _, e := a.GetPartition(-1, "a"); return e }, api.ErrPartitionOutOfRange},
		{"GetPartition(N)", func() error { _, _, _, _, e := a.GetPartition(3, "a"); return e }, api.ErrPartitionOutOfRange},
		{"Put empty", func() error { return a.Put("", "z") }, api.ErrEmptyKey},
		{"Get empty", func() error { _, _, e := a.Get(""); return e }, api.ErrEmptyKey},
	}
	for _, c := range cases {
		if err := c.call(); !errors.Is(err, c.want) {
			t.Errorf("%s: err=%v want %v", c.name, err, c.want)
		}
	}
	if !reflect.DeepEqual(before, a.Dump()) {
		t.Fatalf("rejected ops mutated state: %v vs %v", before, a.Dump())
	}
	if api.ErrInvalidN == api.ErrEmptyKey || api.ErrEmptyKey == api.ErrPartitionOutOfRange || api.ErrInvalidN == api.ErrPartitionOutOfRange {
		t.Fatalf("sentinel errors must be distinct")
	}
	if err := a.Put("f", "F"); err != nil { // 被拒后仍可正常使用
		t.Fatalf("Put after rejections: %v", err)
	}
	if v, ok, _ := a.Get("f"); !ok || v != "F" {
		t.Fatalf("Get after rejections: %q,%v", v, ok)
	}
}

type gres struct {
	v, pv     string
	f, pf, pm bool
	ph        int
}

// TestConcurrentReadOnly 并发只读，结果逐字段相同；不使用 sleep。
func TestConcurrentReadOnly(t *testing.T) {
	a := scenarioRebalanced(t)
	const G = 64
	var wg sync.WaitGroup
	out := make([]gres, G)
	wg.Add(G)
	for g := 0; g < G; g++ {
		go func(i int) {
			defer wg.Done()
			v, f, _ := a.Get("b")
			pv, pf, pm, ph, _ := a.GetPartition(0, "b")
			out[i] = gres{v, pv, f, pf, pm, ph}
		}(g)
	}
	wg.Wait()
	for i := 1; i < G; i++ {
		if out[i] != out[0] {
			t.Fatalf("goroutine %d got %+v, want %+v", i, out[i], out[0])
		}
	}
}

func TestSelfCheck(t *testing.T) {
	if err := scenarioRebalanced(t).SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
