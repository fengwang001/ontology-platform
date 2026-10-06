package reflow

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

type memLogger struct{ sb strings.Builder }

func (l *memLogger) Printf(format string, args ...any) {
	fmt.Fprintf(&l.sb, format+"\n", args...)
}

func changeIDs(changes []Change) []NodeID {
	out := make([]NodeID, len(changes))
	for i, c := range changes {
		out[i] = c.ID
	}
	return out
}

func containsID(xs []NodeID, x NodeID) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func equalIDs(a, b []NodeID) bool {
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

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func newTestKernel(t *testing.T) *Kernel {
	t.Helper()
	k, err := NewKernel(Fixed(100), Fixed(100), 0, 0)
	if err != nil {
		t.Fatalf("NewKernel: %v", err)
	}
	return k
}

func addNode(t *testing.T, k *Kernel, wm, hm Mode, px, py int64, isolated bool) NodeID {
	t.Helper()
	id, err := k.NewNode(wm, hm, px, py, isolated)
	if err != nil {
		t.Fatalf("NewNode: %v", err)
	}
	return id
}

// 边界三条件逐一缺失：隔离缺失、宽非固定、高非固定，脏标记均穿越到根。
func TestBoundaryConditionsMissing(t *testing.T) {
	cases := []struct {
		name     string
		isolated bool
		wm, hm   Mode
	}{
		{"not-isolated", false, Fixed(50), Fixed(50)},
		{"width-auto", true, Auto(), Fixed(50)},
		{"height-auto", true, Fixed(50), Auto()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k := newTestKernel(t)
			b := addNode(t, k, tc.wm, tc.hm, 0, 0, tc.isolated)
			leaf := addNode(t, k, Fixed(10), Fixed(10), 0, 0, false)
			mustOK(t, k.Insert(0, b, 0))
			mustOK(t, k.Insert(b, leaf, 0))
			if _, err := k.Commit(); err != nil {
				t.Fatal(err)
			}
			mustOK(t, k.SetWidth(leaf, Fixed(12)))
			if !k.root.subDirty {
				t.Fatalf("三条件缺一时根应被标记为子树含脏")
			}
		})
	}
}

func TestRootIsBoundary(t *testing.T) {
	k, err := NewKernel(Auto(), Auto(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !k.root.isBoundaryFor(k.root) {
		t.Fatal("根必须始终视为边界")
	}
	leaf := addNode(t, k, Fixed(1), Fixed(1), 0, 0, false)
	mustOK(t, k.Insert(0, leaf, 0))
	_, _ = k.Commit()
	mustOK(t, k.SetHeight(leaf, Fixed(5)))
	if !k.root.subDirty {
		t.Fatal("根应被子树含脏覆盖")
	}
}

func TestSameValueNoDirty(t *testing.T) {
	k := newTestKernel(t)
	a := addNode(t, k, Auto(), Auto(), 1, 2, true)
	mustOK(t, k.Insert(0, a, 0))
	_, _ = k.Commit()
	before := k.stats.MarkPropSteps
	mustOK(t, k.SetPadding(a, 1, 2))
	mustOK(t, k.SetIsolated(a, true))
	mustOK(t, k.SetWidth(a, Auto()))
	mustOK(t, k.SetHeight(a, Auto()))
	if k.stats.MarkPropSteps != before {
		t.Fatalf("同值修改不应产生传播步数：%d -> %d", before, k.stats.MarkPropSteps)
	}
	if k.pending {
		t.Fatal("同值修改不应留下未提交脏标记")
	}
}

// 子变化使父重算后尺寸不变时，上溯在父这一跳终止。
func TestParentSizeUnchangedStops(t *testing.T) {
	k := newTestKernel(t)
	grand := addNode(t, k, Auto(), Auto(), 0, 0, false)
	parent := addNode(t, k, Fixed(30), Fixed(30), 0, 0, false)
	child := addNode(t, k, Auto(), Auto(), 0, 0, false)
	leaf := addNode(t, k, Fixed(5), Fixed(5), 0, 0, false)
	mustOK(t, k.Insert(0, grand, 0))
	mustOK(t, k.Insert(grand, parent, 0))
	mustOK(t, k.Insert(parent, child, 0))
	mustOK(t, k.Insert(child, leaf, 0))
	_, _ = k.Commit()

	mustOK(t, k.SetWidth(leaf, Fixed(9)))
	changes, err := k.Commit()
	if err != nil {
		t.Fatal(err)
	}
	got := changeIDs(changes)
	if containsID(got, grand) {
		t.Fatalf("祖父不应被重算：父尺寸未变应停止上溯，实际 %v", got)
	}
	if !containsID(got, parent) {
		t.Fatalf("父本身仍应被重算，实际 %v", got)
	}
}

// 子尺寸变化穿到边界即止，边界外干净节点不重算。
func TestStopsAtBoundary(t *testing.T) {
	k := newTestKernel(t)
	b := addNode(t, k, Fixed(40), Fixed(40), 0, 0, true)
	mid := addNode(t, k, Auto(), Auto(), 0, 0, false)
	leaf := addNode(t, k, Fixed(4), Fixed(4), 0, 0, false)
	outside := addNode(t, k, Auto(), Auto(), 0, 0, false)
	mustOK(t, k.Insert(0, b, 0))
	mustOK(t, k.Insert(b, mid, 0))
	mustOK(t, k.Insert(mid, leaf, 0))
	mustOK(t, k.Insert(0, outside, 1))
	_, _ = k.Commit()

	mustOK(t, k.SetWidth(leaf, Fixed(8)))
	changes, err := k.Commit()
	if err != nil {
		t.Fatal(err)
	}
	got := changeIDs(changes)
	if containsID(got, outside) {
		t.Fatalf("边界外干净节点不应重算，实际 %v", got)
	}
	for _, want := range []NodeID{leaf, mid, b} {
		if !containsID(got, want) {
			t.Fatalf("节点 %d 应重算，实际 %v", want, got)
		}
	}
}

// 移动带脏子树跨过布局边界：标记保留并按新位置重新传播到新边界。
func TestMoveAcrossBoundaryRepropagation(t *testing.T) {
	k := newTestKernel(t)
	b1 := addNode(t, k, Fixed(40), Fixed(40), 0, 0, true)
	b2 := addNode(t, k, Fixed(60), Fixed(60), 0, 0, true)
	x := addNode(t, k, Auto(), Auto(), 0, 0, false)
	leaf := addNode(t, k, Fixed(3), Fixed(3), 0, 0, false)
	mustOK(t, k.Insert(0, b1, 0))
	mustOK(t, k.Insert(0, b2, 1))
	mustOK(t, k.Insert(b1, x, 0))
	mustOK(t, k.Insert(x, leaf, 0))
	_, _ = k.Commit()

	mustOK(t, k.SetWidth(leaf, Fixed(7)))
	if !k.nodes[b1].subDirty || k.nodes[b2].subDirty {
		t.Fatal("初始脏应只封在 b1 内")
	}
	mustOK(t, k.Move(x, b2, 0))
	if !k.nodes[b2].subDirty {
		t.Fatal("跨边界移动后脏应重新传播到 b2")
	}
	changes, err := k.Commit()
	if err != nil {
		t.Fatal(err)
	}
	got := changeIDs(changes)
	if !containsID(got, b2) || !containsID(got, x) || !containsID(got, leaf) {
		t.Fatalf("新边界与被移动子树都应重算，实际 %v", got)
	}
}

// 摘下的子树脏标记在重新插入前不得丢失，重新插入后照常重算。
func TestRemoveReinsertKeepsDirty(t *testing.T) {
	k := newTestKernel(t)
	p := addNode(t, k, Auto(), Auto(), 0, 0, false)
	leaf := addNode(t, k, Fixed(2), Fixed(2), 0, 0, false)
	mustOK(t, k.Insert(0, p, 0))
	mustOK(t, k.Insert(p, leaf, 0))
	_, _ = k.Commit()
	mustOK(t, k.SetWidth(leaf, Fixed(11)))
	mustOK(t, k.Remove(p))
	_, _ = k.Commit()
	if !k.nodes[leaf].selfDirty || !k.nodes[p].subDirty {
		t.Fatal("摘下子树的脏标记在重新插入前丢失")
	}
	other := addNode(t, k, Auto(), Auto(), 0, 0, false)
	mustOK(t, k.Insert(0, other, 0))
	mustOK(t, k.Insert(other, p, 0))
	changes, err := k.Commit()
	if err != nil {
		t.Fatal(err)
	}
	got := changeIDs(changes)
	for _, want := range []NodeID{leaf, p, other} {
		if !containsID(got, want) {
			t.Fatalf("重新插入后 %d 应重算，实际 %v", want, got)
		}
	}
}

// 边界身份失去与获得两个方向。
func TestBoundaryIdentityChange(t *testing.T) {
	t.Run("lose", func(t *testing.T) {
		k := newTestKernel(t)
		b := addNode(t, k, Fixed(20), Fixed(20), 0, 0, true)
		leaf := addNode(t, k, Fixed(2), Fixed(2), 0, 0, false)
		mustOK(t, k.Insert(0, b, 0))
		mustOK(t, k.Insert(b, leaf, 0))
		_, _ = k.Commit()
		mustOK(t, k.SetWidth(leaf, Fixed(9)))
		if k.root.subDirty {
			t.Fatal("内部边界应挡住脏标记上传")
		}
		mustOK(t, k.SetIsolated(b, false))
		if !k.root.subDirty {
			t.Fatal("失去边界身份后子树脏必须继续上传到根")
		}
	})
	t.Run("gain", func(t *testing.T) {
		k := newTestKernel(t)
		n := addNode(t, k, Fixed(20), Auto(), 0, 0, true)
		leaf := addNode(t, k, Fixed(2), Fixed(2), 0, 0, false)
		mustOK(t, k.Insert(0, n, 0))
		mustOK(t, k.Insert(n, leaf, 0))
		_, _ = k.Commit()
		mustOK(t, k.SetWidth(leaf, Fixed(6)))
		if !k.root.subDirty {
			t.Fatal("非边界期间脏应已到达根")
		}
		mustOK(t, k.SetHeight(n, Fixed(20)))
		if _, ok := k.boundaries[k.nodes[n]]; !ok {
			t.Fatal("获得边界身份后应作为含脏边界参与重排")
		}
		if !k.root.subDirty {
			t.Fatal("获得边界身份不得撤回已传播到祖先的标记")
		}
	})
}

func setupConsistency(t *testing.T) (*Kernel, []NodeID) {
	k := newTestKernel(t)
	a := addNode(t, k, Auto(), Auto(), 0, 0, false)
	c := addNode(t, k, Fixed(4), Fixed(4), 0, 0, false)
	mustOK(t, k.Insert(0, a, 0))
	mustOK(t, k.Insert(a, c, 0))
	_, _ = k.Commit()
	mustOK(t, k.SetWidth(c, Fixed(8)))
	return k, []NodeID{c, a, 0}
}

// 隐式提交与显式提交集合一致；连续无修改查询不重排。
func TestImplicitExplicitConsistency(t *testing.T) {
	k1, expected := setupConsistency(t)
	explicit, err := k1.Commit()
	if err != nil {
		t.Fatal(err)
	}
	if got := changeIDs(explicit); !equalIDs(got, expected) {
		t.Fatalf("显式重排序 %v，期望 %v", got, expected)
	}
	k2, _ := setupConsistency(t)
	k2.stats.RecomputeVisits = 0
	if _, err := k2.Size(0); err != nil {
		t.Fatal(err)
	}
	if k2.stats.RecomputeVisits != int64(len(explicit)) {
		t.Fatalf("隐式重算数=%d 显式=%d", k2.stats.RecomputeVisits, len(explicit))
	}
	before := k2.stats.RecomputeVisits
	_, _ = k2.Size(0)
	_, _ = k2.Size(1)
	if k2.stats.RecomputeVisits != before {
		t.Fatalf("连续无修改查询不得触发重排：%d -> %d", before, k2.stats.RecomputeVisits)
	}
}

// 提交钩子中重入提交：返回可区分的提交中重入错误，且树状态不变。
func TestReentrantCommitRejected(t *testing.T) {
	k := newTestKernel(t)
	a := addNode(t, k, Auto(), Auto(), 0, 0, false)
	mustOK(t, k.Insert(0, a, 0))
	mustOK(t, k.SetPadding(a, 1, 1))
	var got error
	k.ReentryHook = func() {
		_, got = k.Commit()
	}
	if _, err := k.Commit(); err != nil {
		t.Fatalf("外层提交不应失败：%v", err)
	}
	if KindOf(got) != KindReentrantCommit {
		t.Fatalf("期望提交中重入错误，得到 %v", got)
	}
	if k.inCommit {
		t.Fatal("外层提交结束后重入标记必须复位")
	}
}

// 四类错误可区分，拒绝次序：参数非法 > 节点不存在 > 结构冲突 > 重入。
func TestErrorKindsAndPrecedence(t *testing.T) {
	k := newTestKernel(t)
	a := addNode(t, k, Auto(), Auto(), 0, 0, false)
	mustOK(t, k.Insert(0, a, 0))

	if err := k.SetWidth(999, Fixed(-1)); KindOf(err) != KindInvalidArg {
		t.Fatalf("负尺寸+未知节点：应先判参数非法，得到 %v", err)
	}
	if err := k.SetWidth(999, Fixed(1)); KindOf(err) != KindNodeNotFound {
		t.Fatalf("合法参数+未知节点：节点不存在，得到 %v", err)
	}
	if err := k.Insert(a, 0, -1); KindOf(err) != KindInvalidArg {
		t.Fatalf("负下标即使涉及根也应参数非法优先，得到 %v", err)
	}
	if err := k.Remove(0); KindOf(err) != KindConflict {
		t.Fatalf("移除根应为结构冲突，得到 %v", err)
	}
	if err := k.Move(0, a, 0); KindOf(err) != KindConflict {
		t.Fatalf("移动根应为结构冲突，得到 %v", err)
	}
	if err := k.Insert(a, a, 0); KindOf(err) != KindInvalidArg {
		t.Fatalf("自身作为自身子孙应为参数非法，得到 %v", err)
	}
	if err := k.Insert(0, a, 0); KindOf(err) != KindConflict {
		t.Fatalf("插入已有父节点应为结构冲突，得到 %v", err)
	}
	if err := k.SetWidth(0, Mode{Kind: ModeKind(7)}); KindOf(err) != KindInvalidArg {
		t.Fatalf("未知模式应为参数非法，得到 %v", err)
	}

	// 被拒绝操作不改变任何状态。
	s, err := k.Size(0)
	if err != nil {
		t.Fatal(err)
	}
	if s != (Size{W: 100, H: 100}) {
		t.Fatalf("拒绝操作后尺寸不应改变：%v", s)
	}
}

// 并发修改后，每个节点尺寸等于按当前状态的全量重算值。
func TestConcurrentMatchesFullRecompute(t *testing.T) {
	k := newTestKernel(t)
	var ids []NodeID
	for i := 0; i < 20; i++ {
		ids = append(ids, addNode(t, k, Auto(), Auto(), int64(i%3), int64(i%2), i%5 == 0))
	}
	for i, id := range ids {
		mustOK(t, k.Insert(0, id, i))
	}
	if _, err := k.Commit(); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for round := 0; round < 50; round++ {
				id := ids[(g*7+round)%len(ids)]
				switch round % 4 {
				case 0:
					_ = k.SetPadding(id, int64(round%4), int64(round%3))
				case 1:
					if round%2 == 0 {
						_ = k.SetWidth(id, Fixed(int64(g+1)))
					} else {
						_ = k.SetWidth(id, Auto())
					}
				case 2:
					if round%2 == 0 {
						_ = k.SetHeight(id, Fixed(int64(g+2)))
					} else {
						_ = k.SetHeight(id, Auto())
					}
				case 3:
					if _, err := k.Size(id); err != nil {
						t.Errorf("Size: %v", err)
						return
					}
				}
			}
		}(g)
	}
	wg.Wait()
	if _, err := k.Commit(); err != nil {
		t.Fatal(err)
	}
	got := fullRecompute(t, k)
	for _, id := range append([]NodeID{0}, ids...) {
		cur := Size{W: k.nodes[id].w, H: k.nodes[id].h}
		if got[id] != cur {
			t.Fatalf("节点 %d 并发后尺寸 %v 与全量重算 %v 不符",
				id, cur, got[id])
		}
	}
}
