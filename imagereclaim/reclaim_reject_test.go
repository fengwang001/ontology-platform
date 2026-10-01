package imagereclaim

import "testing"

func TestNewConfigInvalid(t *testing.T) {
	bad := [][2]int64{
		{0, 0}, {-1, 0}, {1_000_000_000_000_001, 0}, {1, -1}, {1, 101},
	}
	for _, c := range bad {
		if _, err := New(c[0], int(c[1])); err == nil {
			t.Fatalf("New(%d,%d) want error", c[0], c[1])
		} else if e, ok := err.(*Error); !ok || e.Code != ErrConfigInvalid {
			t.Fatalf("New(%d,%d) = %v, want ErrConfigInvalid", c[0], c[1], err)
		}
	}
	if _, err := New(1, 0); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

// TestRejectionOrder 按规定顺序只报第一个原因。
func TestRejectionOrder(t *testing.T) {
	// 参数非法优先于一切：空镜像 ID、空层列表、空层 ID、层字节越界、层 ID 重复、now<0。
	if _, err := New(1000, 0); err != nil {
		t.Fatal(err)
	}
	r, _ := New(1000, 0)
	mustPull(t, r, "dup", []Layer{{ID: "L", Size: 10}}, 5)

	if err := r.BeginPull("", []Layer{{ID: "L", Size: 1}}, -1); err == nil || err.(*Error).Code != ErrInvalidParam {
		t.Fatalf("empty img must be ErrInvalidParam, got %v", err)
	}
	if err := r.BeginPull("x", nil, 1); err == nil || err.(*Error).Code != ErrInvalidParam {
		t.Fatalf("empty layers must be ErrInvalidParam, got %v", err)
	}
	if err := r.BeginPull("x", []Layer{{ID: "", Size: 1}}, 1); err == nil || err.(*Error).Code != ErrInvalidParam {
		t.Fatalf("empty layer id must be ErrInvalidParam, got %v", err)
	}
	if err := r.BeginPull("x", []Layer{{ID: "L", Size: 0}}, 1); err == nil || err.(*Error).Code != ErrInvalidParam {
		t.Fatalf("size 0 must be ErrInvalidParam, got %v", err)
	}
	if err := r.BeginPull("x", []Layer{{ID: "L", Size: 1_000_000_000_001}}, 1); err == nil || err.(*Error).Code != ErrInvalidParam {
		t.Fatalf("oversize layer must be ErrInvalidParam, got %v", err)
	}
	if err := r.BeginPull("x", []Layer{{ID: "L", Size: 1}, {ID: "L", Size: 1}}, 1); err == nil || err.(*Error).Code != ErrInvalidParam {
		t.Fatalf("duplicate layer in image must be ErrInvalidParam, got %v", err)
	}
	// now<0 也是参数非法（对存在的镜像做 Run）。
	if err := r.Run("dup", -1); err == nil || err.(*Error).Code != ErrInvalidParam {
		t.Fatalf("negative now must be ErrInvalidParam, got %v", err)
	}

	// 时钟回退优先于镜像不存在/状态等：now < 已接受最大 now(5)。
	if err := r.Run("ghost", 4); err == nil || err.(*Error).Code != ErrClockRewind {
		t.Fatalf("clock rewind before not-found, got %v", err)
	}
	if err := r.BeginPull("dup", []Layer{{ID: "X", Size: 1}}, 4); err == nil || err.(*Error).Code != ErrClockRewind {
		t.Fatalf("clock rewind before image-exists, got %v", err)
	}
	if _, err := r.GC(4, 50, 10, 0); err == nil || err.(*Error).Code != ErrClockRewind {
		t.Fatalf("GC clock rewind, got %v", err)
	}

	// 镜像已存在（含拉取中）。
	if err := r.BeginPull("p1", []Layer{{ID: "A", Size: 1}}, 6); err != nil {
		t.Fatal(err)
	}
	if err := r.BeginPull("p1", []Layer{{ID: "A", Size: 1}}, 7); err == nil || err.(*Error).Code != ErrImageExists {
		t.Fatalf("pulling image counts as exists, got %v", err)
	}
	if err := r.Pull("dup", []Layer{{ID: "L", Size: 10}}, 7); err == nil || err.(*Error).Code != ErrImageExists {
		t.Fatalf("ready image exists, got %v", err)
	}

	// 镜像不存在优先于状态不符。
	if err := r.Run("ghost", 8); err == nil || err.(*Error).Code != ErrImageNotFound {
		t.Fatalf("Run missing got %v", err)
	}
	if err := r.Stop("ghost", 8); err == nil || err.(*Error).Code != ErrImageNotFound {
		t.Fatalf("Stop missing got %v", err)
	}
	if err := r.CommitPull("ghost", 8); err == nil || err.(*Error).Code != ErrImageNotFound {
		t.Fatalf("Commit missing got %v", err)
	}
	if err := r.AbortPull("ghost", 8); err == nil || err.(*Error).Code != ErrImageNotFound {
		t.Fatalf("Abort missing got %v", err)
	}

	// 拉取中（Run/Stop）与不在拉取中（Commit/Abort）两种状态不符彼此可区分。
	if err := r.Run("p1", 8); err == nil || err.(*Error).Code != ErrImagePulling {
		t.Fatalf("Run pulling got %v", err)
	}
	if err := r.Stop("p1", 8); err == nil || err.(*Error).Code != ErrImagePulling {
		t.Fatalf("Stop pulling got %v", err)
	}
	if err := r.CommitPull("dup", 8); err == nil || err.(*Error).Code != ErrImageReady {
		t.Fatalf("Commit ready got %v", err)
	}
	if err := r.AbortPull("dup", 8); err == nil || err.(*Error).Code != ErrImageReady {
		t.Fatalf("Abort ready got %v", err)
	}

	// 层冲突带层 ID，优先于空间不足。
	r2, _ := New(100, 0)
	mustPull(t, r2, "a:1", []Layer{{ID: "base", Size: 60}}, 1)
	err := r2.Pull("a:2", []Layer{{ID: "base", Size: 90}, {ID: "big", Size: 1000}}, 2)
	expectErrCode(t, err, ErrLayerConflict, "base")

	// 空间不足：新增层会使 used 超过 C。
	if err := r2.Pull("a:3", []Layer{{ID: "big", Size: 41}}, 2); err == nil || err.(*Error).Code != ErrNoSpace {
		t.Fatalf("no space got %v", err)
	}
	// 恰好放得下（60+40=100）应成功。
	if err := r2.Pull("a:4", []Layer{{ID: "fit", Size: 40}}, 2); err != nil {
		t.Fatalf("exact-fit pull should succeed: %v", err)
	}

	// Stop 未在运行。
	if err := r.Stop("dup", 9); err == nil || err.(*Error).Code != ErrNotRunning {
		t.Fatalf("Stop not running got %v", err)
	}

	// GC 参数非法优先于时钟回退。
	if _, err := r.GC(1, 10, 20, 0); err == nil || err.(*Error).Code != ErrInvalidParam {
		t.Fatalf("low>=high must be ErrInvalidParam, got %v", err)
	}
	if _, err := r.GC(1, 0, 0, 0); err == nil || err.(*Error).Code != ErrInvalidParam {
		t.Fatalf("high=0 must be ErrInvalidParam, got %v", err)
	}
	if _, err := r.GC(1, 101, 0, 0); err == nil || err.(*Error).Code != ErrInvalidParam {
		t.Fatalf("high=101 must be ErrInvalidParam, got %v", err)
	}
	if _, err := r.GC(1, 50, 10, -1); err == nil || err.(*Error).Code != ErrInvalidParam {
		t.Fatalf("minAge<0 must be ErrInvalidParam, got %v", err)
	}
}

// TestRejectedOpsDoNotMutate 被拒绝的操作不改变镜像、层与 used。
func TestRejectedOpsDoNotMutate(t *testing.T) {
	r, _ := New(100, 0)
	mustPull(t, r, "a:1", []Layer{{ID: "S", Size: 60}, {ID: "P", Size: 10}}, 1)
	usedBefore := r.Used()

	// 层冲突：新层引用不应建立。
	_ = r.Pull("a:2", []Layer{{ID: "S", Size: 61}, {ID: "Q", Size: 5}}, 2)
	if r.Used() != usedBefore {
		t.Fatalf("layer conflict mutated used: %d -> %d", usedBefore, r.Used())
	}
	if _, ok := r.layers["Q"]; ok {
		t.Fatal("layer Q must not exist after rejected pull")
	}
	if _, ok := r.images["a:2"]; ok {
		t.Fatal("image a:2 must not exist after rejected pull")
	}
	// 空间不足：所有新层均不落盘。
	_ = r.Pull("a:3", []Layer{{ID: "X", Size: 20}, {ID: "Y", Size: 20}}, 2)
	if r.Used() != usedBefore {
		t.Fatalf("no-space mutated used: %d -> %d", usedBefore, r.Used())
	}
	if _, ok := r.layers["X"]; ok {
		t.Fatal("layer X must not exist after rejected no-space pull")
	}
	// maxNow 不被拒绝操作推进：接受 now=2 仍合法（未发生时钟回退）。
	if err := r.Run("a:1", 2); err != nil {
		t.Fatalf("rejected op must not advance clock: %v", err)
	}
	if r.images["a:1"].run != 1 {
		t.Fatal("a:1 run count wrong")
	}
	// 状态不符的 Commit/Abort、未运行的 Stop 均不改状态。
	_ = r.CommitPull("a:1", 3)
	_ = r.AbortPull("a:1", 3)
	_ = r.Stop("a:1", 3) // run=1，Stop 成功；再次 Stop 应被拒。
	if err := r.Stop("a:1", 4); err.(*Error).Code != ErrNotRunning {
		t.Fatalf("want ErrNotRunning, got %v", err)
	}
	if r.images["a:1"].run != 0 || r.Used() != usedBefore {
		t.Fatalf("rejected state ops mutated state: run=%d used=%d", r.images["a:1"].run, r.Used())
	}
}
