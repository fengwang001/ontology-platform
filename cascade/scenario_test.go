package cascade_test

import (
	"testing"

	"ontology/cascade"
)

func errKind(err error) cascade.Kind {
	if err == nil {
		return 0
	}
	return err.(*cascade.GCError).Kind
}

func mustCreate(t *testing.T, c *cascade.Controller, id string, owners []cascade.OwnerRef, fins ...string) {
	t.Helper()
	if err := c.Create(id, owners, fins); err != nil {
		t.Fatalf("Create(%s) unexpected error: %v", id, err)
	}
}

func ref(id string, blocking ...bool) cascade.OwnerRef {
	return cascade.OwnerRef{OwnerID: id, Blocking: len(blocking) > 0 && blocking[0]}
}

// TestStrategyDependentFates 覆盖三种策略各自的依赖者去向。
func TestStrategyDependentFates(t *testing.T) {
	// 后台：属主立即移除，依赖者连带后台删除并移除。
	c := cascade.New()
	mustCreate(t, c, "a", nil)
	mustCreate(t, c, "b", []cascade.OwnerRef{ref("a")})
	if err := c.Delete("a", cascade.Background); err != nil {
		t.Fatal(err)
	}
	if c.Exists("a") || c.Exists("b") {
		t.Fatalf("background: both should be removed, a=%v b=%v", c.Exists("a"), c.Exists("b"))
	}

	// 孤立：属主移除，依赖者保留且无属主。
	c = cascade.New()
	mustCreate(t, c, "a", nil)
	mustCreate(t, c, "b", []cascade.OwnerRef{ref("a")})
	if err := c.Delete("a", cascade.Orphan); err != nil {
		t.Fatal(err)
	}
	if c.Exists("a") || !c.Exists("b") {
		t.Fatalf("orphan: a gone, b kept expected")
	}
	if obj, _ := c.Get("b"); len(obj.Owners) != 0 || obj.Deleting {
		t.Fatalf("orphan: b should be alive with no owners: %+v", obj)
	}

	// 前台（非阻塞依赖者）：属主可立即移除，依赖者连带后台删除。
	c = cascade.New()
	mustCreate(t, c, "a", nil)
	mustCreate(t, c, "b", []cascade.OwnerRef{ref("a", false)})
	if err := c.Delete("a", cascade.Foreground); err != nil {
		t.Fatal(err)
	}
	if c.Exists("a") || c.Exists("b") {
		t.Fatalf("foreground non-blocking: both removed expected")
	}
}

// TestMultiOwnerSurvival 多属主：存活属主保住依赖者；全部删除后连带删除。
func TestMultiOwnerSurvival(t *testing.T) {
	c := cascade.New()
	mustCreate(t, c, "a", nil)
	mustCreate(t, c, "b", nil)
	mustCreate(t, c, "d", []cascade.OwnerRef{ref("a"), ref("b")})

	if err := c.Delete("a", cascade.Background); err != nil {
		t.Fatal(err)
	}
	if !c.Exists("d") {
		t.Fatalf("d should survive while b alive")
	}
	if obj, _ := c.Get("d"); len(obj.Owners) != 1 || obj.Owners[0].OwnerID != "b" {
		t.Fatalf("d should keep only owner b: %+v", obj.Owners)
	}
	if err := c.Delete("b", cascade.Background); err != nil {
		t.Fatal(err)
	}
	if c.Exists("b") || c.Exists("d") {
		t.Fatalf("after all owners gone, b and d must be removed")
	}
}

// TestForegroundWaitsForBlocking 前台删除等待阻塞依赖者。
func TestForegroundWaitsForBlocking(t *testing.T) {
	c := cascade.New()
	mustCreate(t, c, "a", nil)
	mustCreate(t, c, "b", []cascade.OwnerRef{ref("a", true)}, "fb")
	mustCreate(t, c, "n", []cascade.OwnerRef{ref("a", false)})

	if err := c.Delete("a", cascade.Foreground); err != nil {
		t.Fatal(err)
	}
	if obj, _ := c.Get("a"); !obj.Deleting || obj.Strategy != cascade.Foreground {
		t.Fatalf("a must remain foreground-deleting: %+v", obj)
	}
	if obj, _ := c.Get("b"); !obj.Deleting || obj.Strategy != cascade.Foreground || len(obj.Finalizers) != 1 {
		t.Fatalf("blocking dependent b must be foreground deleting with finalizer: %+v", obj)
	}
	if _, ok := c.Get("n"); ok {
		t.Fatalf("non-blocking dependent n should be cascaded away")
	}

	// 不能向删除中对象新建引用。
	if err := c.Create("late", []cascade.OwnerRef{ref("a", true)}, nil); err == nil {
		t.Fatalf("creating dependent of deleting owner must fail")
	}

	// b 已删除中，重复删除无操作；解除终结器后 a 与 b 一起移除。
	if err := c.Delete("b", cascade.Foreground); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveFinalizer("b", "fb"); err != nil {
		t.Fatal(err)
	}
	if c.Exists("a") || c.Exists("b") {
		t.Fatalf("full foreground chain should be gone after finalizer release")
	}

	// 另起场景：阻塞链上带终结器时全部停留，解除后整条链移除。
	c2 := cascade.New()
	mustCreate(t, c2, "a", nil)
	mustCreate(t, c2, "b", []cascade.OwnerRef{ref("a", true)})
	mustCreate(t, c2, "c", []cascade.OwnerRef{ref("b", true)}, "fc")
	if err := c2.Delete("a", cascade.Foreground); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c"} {
		if obj, ok := c2.Get(id); !ok || !obj.Deleting {
			t.Fatalf("%s should be deleting while finalizer holds", id)
		}
	}
	if err := c2.RemoveFinalizer("c", "fc"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c"} {
		if c2.Exists(id) {
			t.Fatalf("%s must be removed once finalizer released", id)
		}
	}
}

// TestFinalizerBlocksAndUnblocks 终结器阻塞与解除后的整条级联。
func TestFinalizerBlocksAndUnblocks(t *testing.T) {
	c := cascade.New()
	mustCreate(t, c, "a", nil)
	mustCreate(t, c, "b", []cascade.OwnerRef{ref("a")}, "fb")
	mustCreate(t, c, "c", []cascade.OwnerRef{ref("b")})

	if err := c.Delete("a", cascade.Background); err != nil {
		t.Fatal(err)
	}
	// 后台策略下 a 不受依赖者阻塞：立即移除；b 摘掉属主引用、
	// 因零属主连带进入后台删除，但被终结器 fb 阻塞停留。
	if c.Exists("a") {
		t.Fatalf("background a must be removed immediately even with dependent finalizers")
	}
	obj, _ := c.Get("b")
	if !obj.Deleting || obj.Strategy != cascade.Background || len(obj.Finalizers) != 1 {
		t.Fatalf("b should be background deleting with finalizer: %+v", obj)
	}
	if !c.Exists("c") {
		t.Fatalf("c waits: b still exists")
	}

	// 删除中对象不得追加终结器。
	if err := c.AddFinalizer("b", "x"); errKind(err) != cascade.KindConflict {
		t.Fatalf("add finalizer on deleting b should conflict, got %v", err)
	}
	// 移除不存在的终结器报错。
	if err := c.RemoveFinalizer("b", "nope"); errKind(err) != cascade.KindNotFound {
		t.Fatalf("remove missing finalizer should NotFound, got %v", err)
	}
	if err := c.RemoveFinalizer("b", "fb"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"b", "c"} {
		if c.Exists(id) {
			t.Fatalf("%s must be removed after finalizer release", id)
		}
	}
}

// TestRepeatDeleteAndUpgrade 重复删除与策略升级。
func TestRepeatDeleteAndUpgrade(t *testing.T) {
	c := cascade.New()
	mustCreate(t, c, "a", nil, "fa")
	mustCreate(t, c, "b", []cascade.OwnerRef{ref("a", true)})

	if err := c.Delete("a", cascade.Background); err != nil {
		t.Fatal(err)
	}
	first, _ := c.Get("a")
	if err := c.Delete("a", cascade.Orphan); err != nil {
		t.Fatal(err)
	}
	second, _ := c.Get("a")
	if second.Strategy != cascade.Background || second.DeleteAt != first.DeleteAt {
		t.Fatalf("other combos must not change strategy/time: %+v vs %+v", first, second)
	}

	// 升级为前台：a 必须等待阻塞依赖者 b，因此 a 回来存活（删除中）。
	if err := c.Delete("a", cascade.Foreground); err != nil {
		t.Fatal(err)
	}
	if obj, ok := c.Get("a"); !ok || obj.Strategy != cascade.Foreground {
		t.Fatalf("a should upgrade to foreground: %+v", obj)
	}
	// 前台 -> 后台不降级。
	if err := c.Delete("a", cascade.Background); err != nil {
		t.Fatal(err)
	}
	if obj, _ := c.Get("a"); obj.Strategy != cascade.Foreground {
		t.Fatalf("foreground must not downgrade")
	}
	if err := c.RemoveFinalizer("a", "fa"); err != nil {
		t.Fatal(err)
	}
	if c.Exists("a") {
		t.Fatalf("a should be removed once finalizer released after upgrade")
	}
}

// TestCycleAndSelfReference 自引用与环检测。
func TestCycleAndSelfReference(t *testing.T) {
	c := cascade.New()
	mustCreate(t, c, "a", nil)
	mustCreate(t, c, "b", nil)

	if err := c.Create("self", []cascade.OwnerRef{ref("self")}, nil); errKind(err) != cascade.KindCycle {
		t.Fatalf("self reference should be Cycle, got %v", err)
	}
	if err := c.ReplaceOwners("a", []cascade.OwnerRef{ref("b")}); err != nil {
		t.Fatal(err)
	}
	if err := c.ReplaceOwners("b", []cascade.OwnerRef{ref("a")}); errKind(err) != cascade.KindCycle {
		t.Fatalf("cycle a->b->a should be Cycle, got %v", err)
	}
	// 被拒绝操作不得改变状态。
	if obj, _ := c.Get("b"); len(obj.Owners) != 0 {
		t.Fatalf("b owners unchanged expected: %+v", obj.Owners)
	}
	if err := c.Create("x", []cascade.OwnerRef{ref("missing")}, nil); errKind(err) != cascade.KindOwnerMissing {
		t.Fatalf("missing owner should OwnerMissing, got %v", err)
	}
	if err := c.Create("y", []cascade.OwnerRef{{OwnerID: "a", Blocking: true}, {OwnerID: "a", Blocking: false}}, nil); errKind(err) != cascade.KindInvalidArgument {
		t.Fatalf("duplicate owner ref should InvalidArgument, got %v", err)
	}
}

// TestErrorPriority 错误类别优先级。
func TestErrorPriority(t *testing.T) {
	c := cascade.New()
	mustCreate(t, c, "a", nil)
	mustCreate(t, c, "d", nil, "f")

	// 参数非法优先于一切。
	if err := c.Delete("", cascade.Background); errKind(err) != cascade.KindInvalidArgument {
		t.Fatalf("empty id -> InvalidArgument, got %v", err)
	}
	if err := c.Delete("a", cascade.Strategy(99)); errKind(err) != cascade.KindInvalidArgument {
		t.Fatalf("bad strategy -> InvalidArgument, got %v", err)
	}
	// 对象不存在优先于状态冲突。
	if err := c.AddFinalizer("ghost", "x"); errKind(err) != cascade.KindNotFound {
		t.Fatalf("missing object -> NotFound, got %v", err)
	}
	// 删除中对象 -> 状态冲突。
	if err := c.Delete("d", cascade.Background); err != nil {
		t.Fatal(err)
	}
	if err := c.ReplaceOwners("d", []cascade.OwnerRef{ref("a")}); errKind(err) != cascade.KindConflict {
		t.Fatalf("replace owners on deleting -> Conflict, got %v", err)
	}
	// 环优先于属主缺失：a 存活，构造“既成环又缺属主”。
	if err := c.Create("x", []cascade.OwnerRef{ref("x"), ref("missing")}, nil); errKind(err) != cascade.KindCycle {
		t.Fatalf("cycle+missing should report Cycle, got %v", err)
	}
}
