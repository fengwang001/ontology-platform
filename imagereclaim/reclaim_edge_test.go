package imagereclaim

import "testing"

func mustPull(t *testing.T, r *Reclaimer, img string, layers []Layer, now int64) {
	t.Helper()
	if err := r.Pull(img, layers, now); err != nil {
		t.Fatalf("Pull(%s) at %d: %v", img, now, err)
	}
}

func expectErrCode(t *testing.T, err error, code ErrCode, layerID string) {
	t.Helper()
	e, ok := err.(*Error)
	if !ok || e.Code != code || e.LayerID != layerID {
		t.Fatalf("err = %v, want code=%d layer=%q", err, code, layerID)
	}
}

func equalStrings(a, b []string) bool {
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

// TestSpecExample 校验题目核心示例：共享层删除顺序与释放累计。
func TestSpecExample(t *testing.T) {
	r, err := New(1000, 1)
	if err != nil {
		t.Fatal(err)
	}
	mustPull(t, r, "r:1", []Layer{{ID: "L1", Size: 300}}, 10)
	mustPull(t, r, "r:2", []Layer{{ID: "L1", Size: 300}, {ID: "L2", Size: 200}}, 20)
	mustPull(t, r, "r:3", []Layer{{ID: "L3", Size: 100}}, 30)
	if got := r.Used(); got != 600 {
		t.Fatalf("used = %d, want 600", got)
	}
	res, err := r.GC(100, 50, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	want_ := []string{"r:1", "r:2"}
	if !equalStrings(res.Deleted, want_) || res.Freed != 500 || res.Short {
		t.Fatalf("GC = %+v, want deleted=%v freed=500 short=false", res, want_)
	}
	if got := r.Used(); got != 100 {
		t.Fatalf("used after GC = %d, want 100", got)
	}

	r2, _ := New(1000, 2)
	mustPull(t, r2, "r:1", []Layer{{ID: "L1", Size: 300}}, 10)
	mustPull(t, r2, "r:2", []Layer{{ID: "L1", Size: 300}, {ID: "L2", Size: 200}}, 20)
	mustPull(t, r2, "r:3", []Layer{{ID: "L3", Size: 100}}, 30)
	res, err = r2.GC(100, 50, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(res.Deleted, []string{"r:1"}) || res.Freed != 0 || !res.Short {
		t.Fatalf("K=2 GC = %+v, want [r:1] freed=0 short=true", res)
	}
}

// TestGCThresholdBoundary 覆盖 used*100 恰等于 high*C 触发、小 1 不触发与 need 向下取整。
func TestGCThresholdBoundary(t *testing.T) {
	r, _ := New(1000, 0)
	mustPull(t, r, "a:1", []Layer{{ID: "x", Size: 499}}, 1)
	if res, err := r.GC(10, 50, 20, 0); err != nil || len(res.Deleted) != 0 {
		t.Fatalf("499/1000 should not trigger: res=%+v err=%v", res, err)
	}
	mustPull(t, r, "a:2", []Layer{{ID: "y", Size: 1}}, 11)
	if r.Used() != 500 {
		t.Fatalf("used=%d", r.Used())
	}
	res, err := r.GC(12, 50, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(res.Deleted, []string{"a:1"}) || res.Freed != 499 || res.Short {
		t.Fatalf("at-threshold GC = %+v", res)
	}

	// C=999, low=20 -> floor(199.8)=199；used=500 时 need=301。
	r2, _ := New(999, 0)
	mustPull(t, r2, "a:1", []Layer{{ID: "x", Size: 200}}, 1)
	mustPull(t, r2, "a:2", []Layer{{ID: "y", Size: 300}}, 2)
	res2, err := r2.GC(3, 50, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(res2.Deleted, []string{"a:1", "a:2"}) || res2.Freed != 500 || res2.Short {
		t.Fatalf("floor need GC = %+v", res2)
	}
}

// TestMinAgeBoundary 覆盖 now-lastUsed 恰等于 minAge 合格、小 1 不合格。
func TestMinAgeBoundary(t *testing.T) {
	r, _ := New(1000, 0)
	mustPull(t, r, "a:1", []Layer{{ID: "x", Size: 800}}, 10)
	res, err := r.GC(59, 50, 10, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != 0 || !res.Short {
		t.Fatalf("age 49 should be ineligible: %+v", res)
	}
	if _, ok := r.images["a:1"]; !ok {
		t.Fatal("a:1 must survive when too young")
	}
	res, err = r.GC(60, 50, 10, 50)
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(res.Deleted, []string{"a:1"}) || res.Freed != 800 {
		t.Fatalf("age 50 should be eligible: %+v", res)
	}
}

// TestTieBreaks 覆盖候选与保护在 lastUsed 并列时按镜像 ID 字节序（K 切在并列处）。
func TestTieBreaks(t *testing.T) {
	r, _ := New(1000, 0)
	for _, id := range []string{"b:2", "a:2", "a:1"} {
		mustPull(t, r, id, []Layer{{ID: id, Size: 300}}, 5)
	}
	// used=900, low=40 -> need=500：删 a:1(300) 后再删 a:2(累计600) 即停。
	res, err := r.GC(6, 50, 40, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(res.Deleted, []string{"a:1", "a:2"}) || res.Freed != 600 || res.Short {
		t.Fatalf("candidate tie order = %+v", res)
	}

	// K=1 切在 lastUsed 并列处：ID 较小的 a:1 受保护，候选只有 a:2。
	var r2 *Reclaimer
	r2, _ = New(2000, 1)
	mustPull(t, r2, "a:2", []Layer{{ID: "M2", Size: 600}}, 5)
	mustPull(t, r2, "a:1", []Layer{{ID: "M1", Size: 600}}, 5)
	// used=1200, high=55 触发；low=50 -> need=1200-1000=200，仅候选 a:2。
	res, err = r2.GC(6, 55, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(res.Deleted, []string{"a:2"}) || res.Freed != 600 {
		t.Fatalf("protection tie order = %+v", res)
	}
}

// TestProtectedRunningCounts 受保护镜像含运行中者，且运行中者占保护名额。
func TestProtectedRunningCounts(t *testing.T) {
	r, _ := New(1000, 1)
	mustPull(t, r, "a:old", []Layer{{ID: "old", Size: 600}}, 1)
	mustPull(t, r, "a:new", []Layer{{ID: "new", Size: 400}}, 2)
	if err := r.Run("a:new", 3); err != nil {
		t.Fatal(err)
	}
	// need=1000-floor(1000*40/100)=600：仅删 a:old 恰好达成，a:new 继续运行。
	res, err := r.GC(100, 50, 40, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(res.Deleted, []string{"a:old"}) || res.Freed != 600 || res.Short {
		t.Fatalf("running protection = %+v", res)
	}
	// used=400 超 40% 水位但唯一镜像在运行：候选用尽，Short 为真。
	res, err = r.GC(101, 40, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != 0 || !res.Short {
		t.Fatalf("running image must survive: %+v", res)
	}
}

// TestSharedLayerFreedOnLastHolder 共享层在最后一个持有者被删时才计入释放。
func TestSharedLayerFreedOnLastHolder(t *testing.T) {
	r, _ := New(1000, 0)
	mustPull(t, r, "a:1", []Layer{{ID: "S", Size: 300}, {ID: "P", Size: 100}}, 1)
	mustPull(t, r, "a:2", []Layer{{ID: "S", Size: 300}, {ID: "Q", Size: 100}}, 2)
	if r.Used() != 500 {
		t.Fatalf("used=%d", r.Used())
	}
	res, err := r.GC(3, 50, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(res.Deleted, []string{"a:1", "a:2"}) || res.Freed != 500 || res.Short {
		t.Fatalf("shared layer GC = %+v", res)
	}
	if r.Used() != 0 {
		t.Fatalf("used=%d", r.Used())
	}
}

// TestZeroByteImageDeleted 释放 0 字节的镜像照常删除并继续回收。
func TestZeroByteImageDeleted(t *testing.T) {
	r, _ := New(1000, 0)
	mustPull(t, r, "a:1", []Layer{{ID: "S", Size: 600}}, 1)
	mustPull(t, r, "a:2", []Layer{{ID: "S", Size: 600}}, 2)
	if r.Used() != 600 {
		t.Fatalf("used=%d", r.Used())
	}
	res, err := r.GC(3, 50, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(res.Deleted, []string{"a:1", "a:2"}) || res.Freed != 600 || res.Short {
		t.Fatalf("zero-byte delete = %+v", res)
	}
}

// TestPullingLayersOccupyAndShare 拉取中层立即占用、与就绪镜像共享；Abort 只释放独占层。
func TestPullingLayersOccupyAndShare(t *testing.T) {
	r, _ := New(1000, 0)
	mustPull(t, r, "a:1", []Layer{{ID: "S", Size: 300}}, 1)
	if err := r.BeginPull("a:2", []Layer{{ID: "S", Size: 300}, {ID: "P", Size: 200}}, 2); err != nil {
		t.Fatal(err)
	}
	if r.Used() != 500 {
		t.Fatalf("pulling layers must occupy space, used=%d", r.Used())
	}
	res, err := r.GC(3, 50, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	// a:1 就绪可回收，但其 S 被拉取中的 a:2 引用：释放 0 仍照常删除；拉取中镜像不动。
	if !equalStrings(res.Deleted, []string{"a:1"}) || res.Freed != 0 || !res.Short {
		t.Fatalf("GC must keep pulling image, got %+v", res)
	}
	if _, ok := r.images["a:2"]; !ok {
		t.Fatal("pulling a:2 must survive GC")
	}
	if err := r.AbortPull("a:2", 100); err != nil {
		t.Fatal(err)
	}
	if r.Used() != 0 {
		t.Fatalf("after abort of last holder used=%d want 0", r.Used())
	}

	if err := r.BeginPull("a:3", []Layer{{ID: "S", Size: 300}, {ID: "P", Size: 200}}, 101); err != nil {
		t.Fatal(err)
	}
	err = r.BeginPull("a:4", []Layer{{ID: "S", Size: 301}}, 102)
	expectErrCode(t, err, ErrLayerConflict, "S")
	if r.Used() != 500 {
		t.Fatalf("rejected pull changed state, used=%d", r.Used())
	}
}

// TestCandidatesExhaustedShort 候选用尽时不足标记为真，已删镜像保持删除。
func TestCandidatesExhaustedShort(t *testing.T) {
	r, _ := New(1000, 0)
	mustPull(t, r, "a:1", []Layer{{ID: "x", Size: 400}}, 1)
	mustPull(t, r, "a:2", []Layer{{ID: "y", Size: 400}}, 2)
	mustPull(t, r, "keep", []Layer{{ID: "z", Size: 200}}, 3)
	if err := r.Run("keep", 4); err != nil {
		t.Fatal(err)
	}
	// used=1000 恰满，need=1000-floor(1000*1/100)=990；候选仅释放 800。
	res, err := r.GC(100, 100, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(res.Deleted, []string{"a:1", "a:2"}) || res.Freed != 800 || !res.Short {
		t.Fatalf("exhausted = %+v, want freed=800 short=true", res)
	}
	if _, ok := r.images["a:1"]; ok {
		t.Fatal("deleted images must stay deleted")
	}
	if r.Used() != 200 {
		t.Fatalf("used=%d", r.Used())
	}
}

// TestStopRefreshesLastUsed Stop 刷新 lastUsed，从而影响回收次序与 minAge。
func TestStopRefreshesLastUsed(t *testing.T) {
	r, _ := New(1000, 0)
	mustPull(t, r, "a:1", []Layer{{ID: "x", Size: 600}}, 1)
	mustPull(t, r, "a:2", []Layer{{ID: "y", Size: 300}}, 2)
	if err := r.Run("a:1", 3); err != nil {
		t.Fatal(err)
	}
	if err := r.Stop("a:1", 50); err != nil {
		t.Fatal(err)
	}
	// 再次 Stop 必须被拒绝（未在运行），且不推进时钟。
	if err := r.Stop("a:1", 50); err.(*Error).Code != ErrNotRunning {
		t.Fatalf("want ErrNotRunning, got %v", err)
	}
	// a:1 的 lastUsed=50 晚于 a:2=2：先删 a:2。
	// used=900, high=61 触发；low=60 -> need=300：删 a:2 恰好达成。
	res, err := r.GC(51, 61, 60, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(res.Deleted, []string{"a:2"}) || res.Freed != 300 || res.Short {
		t.Fatalf("stop refresh order = %+v", res)
	}
	// 更晚的 GC 才能删除刚刷新过的 a:1。
	// used=600, high=60 触发；low=50 -> need=100：删 a:1 释放 600。
	res, err = r.GC(52, 60, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(res.Deleted, []string{"a:1"}) || res.Freed != 600 || res.Short {
		t.Fatalf("late GC = %+v", res)
	}
}
