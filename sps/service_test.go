package sps

import (
	"errors"
	"testing"
)

func mustResult(t *testing.T, r *UpdateResult, err error) *UpdateResult {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return r
}

func eqInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func wantResult(t *testing.T, r *UpdateResult, ver int, dc, pc []int) {
	t.Helper()
	if r.Version != ver {
		t.Fatalf("version = %d, want %d", r.Version, ver)
	}
	if !eqInts(r.DChanged, dc) {
		t.Fatalf("v%d DChanged = %v, want %v", ver, r.DChanged, dc)
	}
	if !eqInts(r.PChanged, pc) {
		t.Fatalf("v%d PChanged = %v, want %v", ver, r.PChanged, pc)
	}
}

func wantDist(t *testing.T, svc *Service, v int, d int64, ok bool) {
	t.Helper()
	got, gotOK, err := svc.Dist(v)
	if err != nil || got != d || gotOK != ok {
		t.Fatalf("Dist(%d) = (%d,%v,%v), want (%d,%v)", v, got, gotOK, err, d, ok)
	}
}

// TestSpecExample 复现题目描述的 8 个版本。
func TestSpecExample(t *testing.T) {
	svc, err := New(4, 0, 100, 3)
	if err != nil {
		t.Fatal(err)
	}

	r1, err := svc.AddEdge(0, 1, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r1.EdgeID != 1 {
		t.Fatalf("edge id = %d, want 1", r1.EdgeID)
	}
	wantResult(t, r1, 1, []int{1}, nil)

	r2, err := svc.AddEdge(0, 2, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r2.EdgeID != 2 {
		t.Fatalf("edge id = %d, want 2", r2.EdgeID)
	}
	wantResult(t, r2, 2, []int{2}, nil)

	r3, err := svc.AddEdge(2, 1, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r3.EdgeID != 3 {
		t.Fatalf("edge id = %d, want 3", r3.EdgeID)
	}
	wantResult(t, r3, 3, nil, nil) // 边3也紧，但编号1更小

	r4, err := svc.AddEdge(1, 3, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r4.EdgeID != 4 {
		t.Fatalf("edge id = %d, want 4", r4.EdgeID)
	}
	wantResult(t, r4, 4, []int{3}, nil)
	wantDist(t, svc, 3, 6, true)

	// 版本5：边1增权到7，不再紧；边3成为编号最小紧边，父边切换。
	r5, err := svc.SetWeight(1, 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantResult(t, r5, 5, nil, []int{1})
	wantDist(t, svc, 1, 5, true)
	if p, ok, _ := svc.Parent(1); !ok || p != 3 {
		t.Fatalf("parent(1) = (%d,%v), want (3,true)", p, ok)
	}

	// 版本6：边1降回5，重新变紧，父边切回1。
	r6, err := svc.SetWeight(1, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantResult(t, r6, 6, nil, []int{1})
	if p, ok, _ := svc.Parent(1); !ok || p != 1 {
		t.Fatalf("parent(1) = (%d,%v), want (1,true)", p, ok)
	}

	// 版本7：删除边1，父边切到3。
	r7, err := svc.RemoveEdge(1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantResult(t, r7, 7, nil, []int{1})

	// 版本8：边3增权到4，节点1失去唯一紧边并连带节点3。
	r8, err := svc.SetWeight(3, 4)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantResult(t, r8, 8, []int{1, 3}, nil)
	wantDist(t, svc, 1, 6, true)
	wantDist(t, svc, 3, 7, true)

	// 历史查询（K=3，当前版本8，可查 6..8）。
	if d, ok, _ := svc.DistAt(1, 6); !ok || d != 5 {
		t.Fatalf("DistAt(1,6) = (%d,%v), want (5,true)", d, ok)
	}
	if d, ok, _ := svc.DistAt(1, 8); !ok || d != 6 {
		t.Fatalf("DistAt(1,8) = (%d,%v), want (6,true)", d, ok)
	}
	if _, _, err := svc.DistAt(1, 5); !errors.Is(err, ErrVersionStale) {
		t.Fatalf("DistAt(1,5) err = %v, want ErrVersionStale", err)
	}
	if _, _, err := svc.DistAt(1, 9); !errors.Is(err, ErrVersionFuture) {
		t.Fatalf("DistAt(1,9) err = %v, want ErrVersionFuture", err)
	}
	// 版本0已滑出 K=3 窗口：报告历史过期。
	if _, _, err := svc.DistAt(1, 0); !errors.Is(err, ErrVersionStale) {
		t.Fatalf("DistAt(1,0) err = %v, want ErrVersionStale", err)
	}
}

func TestInvalidArguments(t *testing.T) {
	if _, err := New(0, 0, 1, 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("N=0: %v", err)
	}
	if _, err := New(2001, 0, 1, 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("N=2001: %v", err)
	}
	if _, err := New(3, 3, 1, 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("bad source: %v", err)
	}
	if _, err := New(3, 0, 0, 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("bad emax: %v", err)
	}
	if _, err := New(3, 0, 1, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("bad k: %v", err)
	}
	if _, err := New(3, 0, 100001, 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("emax=100001: %v", err)
	}
	if _, err := New(3, 0, 1, 65); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("k=65: %v", err)
	}

	svc, _ := New(2, 0, 1, 1)
	// 节点越界优先于容量检查（容量也已满不了，这里先造满）。
	if _, err := svc.AddEdge(0, 2, 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("v out of range: %v", err)
	}
	if _, err := svc.AddEdge(0, 0, 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("u==v: %v", err)
	}
	if _, err := svc.AddEdge(0, 1, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("w=0: %v", err)
	}
	if _, err := svc.AddEdge(0, 1, 1_000_001); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("w too big: %v", err)
	}

	// 容量已满。
	if _, err := svc.AddEdge(0, 1, 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := svc.AddEdge(1, 0, 1); !errors.Is(err, ErrCapacityFull) {
		t.Fatalf("capacity: %v", err)
	}
	// 容量已满时若参数同时非法，参数非法优先。
	if _, err := svc.AddEdge(0, 5, 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid before capacity: %v", err)
	}

	// 边不存在；非法权重优先于边不存在。
	if _, err := svc.SetWeight(99, 1); !errors.Is(err, ErrEdgeNotFound) {
		t.Fatalf("setweight missing: %v", err)
	}
	if _, err := svc.SetWeight(99, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid weight before edge missing: %v", err)
	}
	if _, err := svc.RemoveEdge(99); !errors.Is(err, ErrEdgeNotFound) {
		t.Fatalf("remove missing: %v", err)
	}

	// DistAt 错误优先级：节点非法 > 版本未产生 > 历史过期。
	if _, _, err := svc.DistAt(5, 99); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("DistAt bad node: %v", err)
	}
	if _, _, err := svc.DistAt(0, 99); !errors.Is(err, ErrVersionFuture) {
		t.Fatalf("DistAt future: %v", err)
	}

	// 被拒绝操作不消耗编号、不升版本。
	if v := svc.Version(); v != 1 {
		t.Fatalf("version after rejects = %d, want 1", v)
	}
}

// TestRejectedNoIDConsumed 容量满后删除再添加，编号严格递增不复用。
func TestRejectedNoIDConsumed(t *testing.T) {
	svc, _ := New(3, 0, 1, 4)
	r, err := svc.AddEdge(0, 1, 5) // id1 v1
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = r
	if _, err := svc.AddEdge(0, 2, 5); !errors.Is(err, ErrCapacityFull) {
		t.Fatalf("capacity: %v", err)
	}
	if _, err := svc.RemoveEdge(1); err != nil { // v2
		t.Fatalf("unexpected error: %v", err)
	}
	r2, err := svc.AddEdge(1, 2, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r2.EdgeID != 2 {
		t.Fatalf("new edge id = %d, want 2 (rejected add must not consume)", r2.EdgeID)
	}
	if v := svc.Version(); v != 3 {
		t.Fatalf("version = %d, want 3", v)
	}
	// 已删除边不可再操作。
	if _, err := svc.SetWeight(1, 1); !errors.Is(err, ErrEdgeNotFound) {
		t.Fatalf("deleted edge: %v", err)
	}
	if _, err := svc.RemoveEdge(1); !errors.Is(err, ErrEdgeNotFound) {
		t.Fatalf("deleted edge: %v", err)
	}
}

// TestSetWeightSameBumpsVersion 设成原权重同样被接受且升版本。
func TestSetWeightSameBumpsVersion(t *testing.T) {
	svc, _ := New(3, 0, 10, 4)
	if _, err := svc.AddEdge(0, 1, 5); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r, err := svc.SetWeight(1, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Version != 2 {
		t.Fatalf("version = %d, want 2", r.Version)
	}
	if len(r.DChanged) != 0 || len(r.PChanged) != 0 {
		t.Fatalf("same weight should report no changes, got %+v", r)
	}
}
