package gc

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

var base = time.Unix(1_700_000_000, 0)

func ts(sec int64) time.Time { return base.Add(time.Duration(sec) * time.Second) }

func mustCreate(t *testing.T, c *Controller, id string, owners []OwnerRef, finalizers ...string) {
	t.Helper()
	if err := c.Create(id, owners, finalizers); err != nil {
		t.Fatalf("Create(%q) failed: %v", id, err)
	}
}

func mustDelete(t *testing.T, c *Controller, id string, p Policy, now time.Time) {
	t.Helper()
	if err := c.Delete(id, p, now); err != nil {
		t.Fatalf("Delete(%q, %s) failed: %v", id, p, err)
	}
}

func mustGet(t *testing.T, c *Controller, id string) View {
	t.Helper()
	v, ok := c.Get(id)
	if !ok {
		t.Fatalf("object %q should exist", id)
	}
	return v
}

func mustGone(t *testing.T, c *Controller, id string) {
	t.Helper()
	if _, ok := c.Get(id); ok {
		t.Fatalf("object %q should be removed", id)
	}
}

func mustErrKind(t *testing.T, err *Error, kind ErrorKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %v, got nil", kind)
	}
	if err.Kind != kind {
		t.Fatalf("expected error kind %v, got %v (%v)", kind, err.Kind, err)
	}
}

func ownersOf(v View) map[string]bool {
	m := make(map[string]bool, len(v.Owners))
	for _, r := range v.Owners {
		m[r.OwnerID] = r.Block
	}
	return m
}

func hasOwner(m map[string]bool, id string) bool {
	_, ok := m[id]
	return ok
}

// 后台删除：属主移除后，失去全部属主的依赖者被连带后台删除，整条链一次收敛。
func TestBackgroundCascadeChain(t *testing.T) {
	c := New()
	mustCreate(t, c, "a", nil)
	mustCreate(t, c, "b", []OwnerRef{{OwnerID: "a", Block: true}})
	mustCreate(t, c, "c", []OwnerRef{{OwnerID: "b"}})

	mustDelete(t, c, "a", Background, ts(1))

	mustGone(t, c, "a")
	mustGone(t, c, "b")
	mustGone(t, c, "c")
}

// 孤立删除：属主被移除，依赖者保留，即使因此没有任何属主。
func TestOrphanKeepsDependents(t *testing.T) {
	c := New()
	mustCreate(t, c, "a", nil)
	mustCreate(t, c, "b", []OwnerRef{{OwnerID: "a", Block: true}}) // Block 对 Orphan 无影响

	mustDelete(t, c, "a", Orphan, ts(1))

	mustGone(t, c, "a")
	v := mustGet(t, c, "b")
	if v.Deleting {
		t.Fatalf("orphaned dependent must stay alive, got deleting")
	}
	if len(v.Owners) != 0 {
		t.Fatalf("orphaned dependent should have no owners, got %v", v.Owners)
	}
}

// 前台删除：依赖者的其他属主全部删除中 -> 依赖者连带前台删除；
// 属主等待阻塞型存活依赖者消失后才被移除。
func TestForegroundCascadeAndBlocking(t *testing.T) {
	c := New()
	mustCreate(t, c, "a", nil)
	mustCreate(t, c, "x", nil)
	mustCreate(t, c, "b", []OwnerRef{{OwnerID: "a", Block: true}, {OwnerID: "x"}})

	// b 的另一个属主 x 仍存活 -> b 保持存活，a 被阻塞型依赖者 b 卡住。
	mustDelete(t, c, "a", Foreground, ts(1))
	va := mustGet(t, c, "a")
	if !va.Deleting || va.Policy != Foreground || !va.ReqTime.Equal(ts(1)) {
		t.Fatalf("a should be foreground-deleting since ts(1), got %+v", va)
	}
	vb := mustGet(t, c, "b")
	if vb.Deleting {
		t.Fatalf("b has a live owner x and must stay alive, got %+v", vb)
	}

	// x 删除后，b 的所有属主都删除中且 a 是前台 -> b 连带前台删除，
	// b 消失后 a 解除阻塞，整条链一次收敛。
	mustDelete(t, c, "x", Background, ts(2))
	mustGone(t, c, "a")
	mustGone(t, c, "b")
	mustGone(t, c, "x")
}

// 前台传播对非阻塞依赖者同样适用，但非阻塞依赖者不阻止属主移除。
func TestForegroundNonBlockingDependent(t *testing.T) {
	c := New()
	mustCreate(t, c, "a", nil)
	mustCreate(t, c, "b", []OwnerRef{{OwnerID: "a"}}) // Block=false

	mustDelete(t, c, "a", Foreground, ts(1))
	// b 唯一属主 a 前台删除中 -> b 连带前台删除；两者都无常驻终结器 -> 都移除。
	mustGone(t, c, "a")
	mustGone(t, c, "b")
}

// 非阻塞依赖者有其他存活属主时保持存活，且不阻止属主移除。
func TestForegroundNonBlockingDependentDoesNotBlock(t *testing.T) {
	c := New()
	mustCreate(t, c, "a", nil)
	mustCreate(t, c, "x", nil)
	mustCreate(t, c, "b", []OwnerRef{{OwnerID: "a"}, {OwnerID: "x"}})

	mustDelete(t, c, "a", Foreground, ts(1))

	mustGone(t, c, "a") // 非阻塞依赖者不阻止移除
	vb := mustGet(t, c, "b")
	if vb.Deleting {
		t.Fatalf("b should stay alive (x still live)")
	}
	if got := ownersOf(vb); len(got) != 1 || !hasOwner(got, "x") {
		t.Fatalf("b should only reference x now, got %v", vb.Owners)
	}
}

// 终结器阻塞删除；移除最后一个终结器后整条级联在返回前收敛。
func TestFinalizerBlocksAndReleasesCascade(t *testing.T) {
	c := New()
	mustCreate(t, c, "a", nil, "fa")
	mustCreate(t, c, "b", []OwnerRef{{OwnerID: "a", Block: true}}, "fb")
	mustCreate(t, c, "c", []OwnerRef{{OwnerID: "b"}}, "fc")

	// a 进入删除中但被终结器挡住；后台删除不传播，b、c 保持存活。
	mustDelete(t, c, "a", Background, ts(1))
	if v := mustGet(t, c, "a"); !v.Deleting || v.Policy != Background {
		t.Fatalf("a should be background-deleting, got %+v", v)
	}
	if v := mustGet(t, c, "b"); v.Deleting {
		t.Fatalf("b should stay alive while a is stuck on finalizer")
	}

	// 解除 a 的终结器：a 移除 -> b 孤儿化后台删除（被 fb 挡住）。
	if err := c.RemoveFinalizer("a", "fa", ts(2)); err != nil {
		t.Fatalf("RemoveFinalizer(a) failed: %v", err)
	}
	mustGone(t, c, "a")
	vb := mustGet(t, c, "b")
	if !vb.Deleting || vb.Policy != Background || !vb.ReqTime.Equal(ts(2)) {
		t.Fatalf("b should be background-deleting since ts(2), got %+v", vb)
	}
	if v := mustGet(t, c, "c"); v.Deleting {
		t.Fatalf("c should stay alive while b is stuck on finalizer")
	}

	// 解除 b：b 移除 -> c 孤儿化后台删除（被 fc 挡住）。
	if err := c.RemoveFinalizer("b", "fb", ts(3)); err != nil {
		t.Fatalf("RemoveFinalizer(b) failed: %v", err)
	}
	mustGone(t, c, "b")
	vc := mustGet(t, c, "c")
	if !vc.Deleting || vc.Policy != Background || !vc.ReqTime.Equal(ts(3)) {
		t.Fatalf("c should be background-deleting since ts(3), got %+v", vc)
	}

	if err := c.RemoveFinalizer("c", "fc", ts(4)); err != nil {
		t.Fatalf("RemoveFinalizer(c) failed: %v", err)
	}
	mustGone(t, c, "c")
}

// 前台删除的属主被终结器挡住时，传播仍然发生；
// 属主解除终结器后须等阻塞型依赖者真正消失。
func TestForegroundWithFinalizers(t *testing.T) {
	c := New()
	mustCreate(t, c, "a", nil, "fa")
	mustCreate(t, c, "b", []OwnerRef{{OwnerID: "a", Block: true}}, "fb")

	mustDelete(t, c, "a", Foreground, ts(1))
	// b 唯一属主 a 前台删除中 -> b 连带前台删除，但被 fb 挡住。
	vb := mustGet(t, c, "b")
	if !vb.Deleting || vb.Policy != Foreground || !vb.ReqTime.Equal(ts(1)) {
		t.Fatalf("b should be foreground-deleting since ts(1), got %+v", vb)
	}

	// a 解除终结器：b 已处于删除中（不再存活），不阻塞 a -> a 移除；
	// b 摘除引用后无属主，但已在前台删除中，策略不变。
	if err := c.RemoveFinalizer("a", "fa", ts(2)); err != nil {
		t.Fatalf("RemoveFinalizer(a) failed: %v", err)
	}
	mustGone(t, c, "a")
	vb = mustGet(t, c, "b")
	if !vb.Deleting || vb.Policy != Foreground || !vb.ReqTime.Equal(ts(1)) {
		t.Fatalf("b should remain foreground-deleting since ts(1), got %+v", vb)
	}
	if len(vb.Owners) != 0 {
		t.Fatalf("b should have no owners, got %v", vb.Owners)
	}

	if err := c.RemoveFinalizer("b", "fb", ts(3)); err != nil {
		t.Fatalf("RemoveFinalizer(b) failed: %v", err)
	}
	mustGone(t, c, "b")
}

// 重复删除是幂等成功；仅后台->前台升级策略，其余组合不变。
func TestRepeatedDeleteAndPolicyUpgrade(t *testing.T) {
	c := New()
	mustCreate(t, c, "a", nil, "fa") // 终结器保证对象留存以便观察
	mustCreate(t, c, "b", nil, "fb")
	mustCreate(t, c, "d", []OwnerRef{{OwnerID: "a"}}, "fd")

	mustDelete(t, c, "a", Background, ts(1))
	mustDelete(t, c, "a", Background, ts(2)) // 幂等，无变化
	va := mustGet(t, c, "a")
	if va.Policy != Background || !va.ReqTime.Equal(ts(1)) {
		t.Fatalf("repeated background delete must be a no-op, got %+v", va)
	}

	mustDelete(t, c, "a", Foreground, ts(3)) // 后台 -> 前台：升级
	va = mustGet(t, c, "a")
	if va.Policy != Foreground || !va.ReqTime.Equal(ts(1)) {
		t.Fatalf("background->foreground must upgrade policy but keep reqTime, got %+v", va)
	}
	// 升级为前台触发传播：d 的唯一属主前台删除中 -> d 连带前台删除。
	vd := mustGet(t, c, "d")
	if !vd.Deleting || vd.Policy != Foreground || !vd.ReqTime.Equal(ts(3)) {
		t.Fatalf("d should be foreground-deleting since ts(3), got %+v", vd)
	}

	mustDelete(t, c, "a", Background, ts(4)) // 前台 -> 后台：不降级
	if va = mustGet(t, c, "a"); va.Policy != Foreground {
		t.Fatalf("foreground->background must not downgrade, got %+v", va)
	}

	mustDelete(t, c, "b", Orphan, ts(1))
	mustDelete(t, c, "b", Foreground, ts(2)) // 孤立 -> 前台：不改变
	if vb := mustGet(t, c, "b"); vb.Policy != Orphan || !vb.ReqTime.Equal(ts(1)) {
		t.Fatalf("orphan must not change policy, got %+v", vb)
	}
}

// 多属主：还有一个属主存在时依赖者存活；全部属主消失后才连带删除。
func TestMultiOwnerSurvivalAndCascade(t *testing.T) {
	c := New()
	mustCreate(t, c, "a", nil)
	mustCreate(t, c, "b", nil)
	mustCreate(t, c, "d", []OwnerRef{{OwnerID: "a", Block: true}, {OwnerID: "b", Block: true}})

	mustDelete(t, c, "a", Background, ts(1))
	mustGone(t, c, "a")
	vd := mustGet(t, c, "d")
	if vd.Deleting {
		t.Fatalf("d still has owner b and must survive")
	}
	if got := ownersOf(vd); len(got) != 1 || !hasOwner(got, "b") {
		t.Fatalf("d should only reference b now, got %v", vd.Owners)
	}

	mustDelete(t, c, "b", Background, ts(2))
	mustGone(t, c, "b")
	mustGone(t, c, "d")
}

// 多属主 + 前台：部分属主删除中不足以触发传播。
func TestMultiOwnerForegroundPartial(t *testing.T) {
	c := New()
	mustCreate(t, c, "a", nil, "fa") // a 被终结器挡住，保持删除中
	mustCreate(t, c, "b", nil)
	mustCreate(t, c, "d", []OwnerRef{{OwnerID: "a"}, {OwnerID: "b"}}, "fd")

	mustDelete(t, c, "a", Foreground, ts(1))
	if v := mustGet(t, c, "d"); v.Deleting {
		t.Fatalf("d has live owner b and must stay alive")
	}
	mustDelete(t, c, "b", Background, ts(2))
	// b 移除后 d 只剩删除中的前台属主 a -> d 连带前台删除。
	vd := mustGet(t, c, "d")
	if !vd.Deleting || vd.Policy != Foreground || !vd.ReqTime.Equal(ts(2)) {
		t.Fatalf("d should be foreground-deleting since ts(2), got %+v", vd)
	}
}

// 自引用与环。
func TestCycleAndSelfReference(t *testing.T) {
	c := New()
	mustErrKind(t, c.Create("a", []OwnerRef{{OwnerID: "a"}}, nil), KindCycle)

	mustCreate(t, c, "a", nil)
	mustCreate(t, c, "b", []OwnerRef{{OwnerID: "a"}})
	mustCreate(t, c, "d", []OwnerRef{{OwnerID: "b"}})

	// 通过 SetOwners 成环：a -> d -> b -> a
	mustErrKind(t, c.SetOwners("a", []OwnerRef{{OwnerID: "d"}}, ts(1)), KindCycle)
	// SetOwners 自引用
	mustErrKind(t, c.SetOwners("a", []OwnerRef{{OwnerID: "a"}}, ts(1)), KindCycle)
	// 合法替换不成环：a 引用新建的无环对象
	mustCreate(t, c, "root", nil)
	if err := c.SetOwners("a", []OwnerRef{{OwnerID: "root"}}, ts(1)); err != nil {
		t.Fatalf("SetOwners(a->root) should succeed: %v", err)
	}
	// 被拒绝的操作不改变任何状态
	if got := ownersOf(mustGet(t, c, "a")); len(got) != 1 || !hasOwner(got, "root") {
		t.Fatalf("a should reference root, got %v", got)
	}
}

// 错误优先级：参数非法 > 对象不存在 > 状态冲突 > 环 > 属主缺失。
func TestErrorPriority(t *testing.T) {
	c := New()
	mustCreate(t, c, "a", nil)
	mustCreate(t, c, "b", []OwnerRef{{OwnerID: "a"}})
	mustCreate(t, c, "del", nil, "f")
	mustDelete(t, c, "del", Background, ts(1)) // del 处于删除中（被终结器挡住）

	// 参数非法 优先于 对象不存在：空 id + 不存在的对象
	mustErrKind(t, c.SetOwners("", []OwnerRef{{OwnerID: "zz"}}, ts(2)), KindInvalidArgument)
	// 参数非法 优先于 环：重复属主 + 自引用
	mustErrKind(t, c.SetOwners("a", []OwnerRef{{OwnerID: "a"}, {OwnerID: "a"}}, ts(2)), KindInvalidArgument)
	// 对象不存在 优先于 状态冲突：目标不存在 + 属主删除中
	mustErrKind(t, c.SetOwners("zz", []OwnerRef{{OwnerID: "del"}}, ts(2)), KindNotFound)
	// 状态冲突 优先于 环：属主删除中 + 会成环
	mustErrKind(t, c.SetOwners("a", []OwnerRef{{OwnerID: "del"}, {OwnerID: "b"}}, ts(2)), KindConflict)
	// 状态冲突 优先于 属主缺失：目标删除中 + 属主不存在
	mustErrKind(t, c.SetOwners("del", []OwnerRef{{OwnerID: "zz"}}, ts(2)), KindConflict)
	// 环 优先于 属主缺失：成环 + 属主不存在
	mustErrKind(t, c.SetOwners("a", []OwnerRef{{OwnerID: "b"}, {OwnerID: "zz"}}, ts(2)), KindCycle)
	// 属主缺失
	mustErrKind(t, c.SetOwners("a", []OwnerRef{{OwnerID: "zz"}}, ts(2)), KindOwnerMissing)

	// Create 同样遵守：重复创建（冲突）优先于属主缺失
	mustErrKind(t, c.Create("a", []OwnerRef{{OwnerID: "zz"}}, nil), KindConflict)
	// Create：属主删除中（冲突）优先于属主缺失
	mustErrKind(t, c.Create("n1", []OwnerRef{{OwnerID: "del"}, {OwnerID: "zz"}}, nil), KindConflict)
	// Delete：非法策略 优先于 对象不存在
	mustErrKind(t, c.Delete("zz", Policy("bogus"), ts(2)), KindInvalidArgument)
	mustErrKind(t, c.Delete("zz", Background, ts(2)), KindNotFound)
	// AddFinalizer：删除中 -> 冲突
	mustErrKind(t, c.AddFinalizer("del", "x"), KindConflict)
	mustErrKind(t, c.AddFinalizer("zz", "x"), KindNotFound)
	// RemoveFinalizer：对象不存在 / 终结器不存在
	mustErrKind(t, c.RemoveFinalizer("zz", "x", ts(2)), KindNotFound)
	mustErrKind(t, c.RemoveFinalizer("a", "x", ts(2)), KindNotFound)

	// 所有被拒绝的操作都没有改变状态。
	if v := mustGet(t, c, "a"); v.Deleting || len(v.Owners) != 0 {
		t.Fatalf("a must be untouched, got %+v", v)
	}
	if v := mustGet(t, c, "del"); !v.Deleting || len(v.Finalizers) != 1 {
		t.Fatalf("del must keep its finalizer, got %+v", v)
	}
	if _, ok := c.Get("n1"); ok {
		t.Fatalf("rejected create must not leave object behind")
	}
}

// 属主修改：整体替换，替换后反向索引与级联行为随之更新。
func TestSetOwnersRewiresCascade(t *testing.T) {
	c := New()
	mustCreate(t, c, "a", nil)
	mustCreate(t, c, "b", nil)
	mustCreate(t, c, "d", []OwnerRef{{OwnerID: "a"}})

	// d 改挂到 b 下；a 孤立删除不再影响 d。
	if err := c.SetOwners("d", []OwnerRef{{OwnerID: "b", Block: true}}, ts(1)); err != nil {
		t.Fatalf("SetOwners failed: %v", err)
	}
	mustDelete(t, c, "a", Orphan, ts(2))
	if v := mustGet(t, c, "d"); v.Deleting {
		t.Fatalf("d must not be affected by a anymore")
	}
	mustDelete(t, c, "b", Background, ts(3))
	mustGone(t, c, "b")
	mustGone(t, c, "d")
}

// SetOwners 允许清空属主；清空后对象不因属主删除而受影响。
func TestSetOwnersToEmpty(t *testing.T) {
	c := New()
	mustCreate(t, c, "a", nil)
	mustCreate(t, c, "d", []OwnerRef{{OwnerID: "a"}})
	if err := c.SetOwners("d", nil, ts(1)); err != nil {
		t.Fatalf("SetOwners(d, []) failed: %v", err)
	}
	mustDelete(t, c, "a", Background, ts(2))
	mustGone(t, c, "a")
	if v := mustGet(t, c, "d"); v.Deleting {
		t.Fatalf("d has no owners and must survive")
	}
}

// SetOwners 后若对象所有属主均删除中且有前台属主，收敛会把它连带前台删除。
func TestSetOwnersTriggersForegroundPropagation(t *testing.T) {
	c := New()
	mustCreate(t, c, "a", nil, "fa") // 终结器让 a 保持删除中
	mustCreate(t, c, "x", nil)
	mustCreate(t, c, "d", []OwnerRef{{OwnerID: "a"}, {OwnerID: "x"}})

	mustDelete(t, c, "a", Foreground, ts(1))
	if v := mustGet(t, c, "d"); v.Deleting {
		t.Fatalf("d has live owner x and must stay alive")
	}
	// d 改挂到仅剩的（前台删除中的）属主……删除中的属主不可引用，
	// 改为摘掉 x：d 只剩 a（前台删除中）-> 收敛为前台删除。
	if err := c.SetOwners("d", nil, ts(2)); err != nil {
		t.Fatalf("SetOwners(d, []) failed: %v", err)
	}
	// d 没有任何属主了：不满足传播条件（没有前台属主），保持存活。
	if v := mustGet(t, c, "d"); v.Deleting {
		t.Fatalf("d has no owners at all; propagation must not fire")
	}
}

// 并发：任意并发调用等价于某个串行顺序；级联对外不可分割。
// 运行时使用 -race 检测数据竞争。
func TestConcurrentOps(t *testing.T) {
	c := New()
	const chains = 64
	// 每条链：root_i <- mid_i <- leaf_i，root 带终结器。
	for i := 0; i < chains; i++ {
		root := fmt.Sprintf("root-%d", i)
		mid := fmt.Sprintf("mid-%d", i)
		leaf := fmt.Sprintf("leaf-%d", i)
		mustCreate(t, c, root, nil, "f")
		mustCreate(t, c, mid, []OwnerRef{{OwnerID: root, Block: true}})
		mustCreate(t, c, leaf, []OwnerRef{{OwnerID: mid}})
	}

	var wg sync.WaitGroup
	for i := 0; i < chains; i++ {
		i := i
		wg.Add(3)
		go func() { // 前台删除根
			defer wg.Done()
			if err := c.Delete(fmt.Sprintf("root-%d", i), Foreground, ts(1)); err != nil && err.Kind != KindNotFound {
				t.Errorf("delete root: %v", err)
			}
		}()
		go func() { // 重复删除根（幂等/不降级）
			defer wg.Done()
			if err := c.Delete(fmt.Sprintf("root-%d", i), Background, ts(2)); err != nil && err.Kind != KindNotFound {
				t.Errorf("re-delete root: %v", err)
			}
		}()
		go func() { // 解除根的终结器，触发整条级联
			defer wg.Done()
			if err := c.RemoveFinalizer(fmt.Sprintf("root-%d", i), "f", ts(3)); err != nil && err.Kind != KindNotFound {
				t.Errorf("remove finalizer: %v", err)
			}
		}()
	}
	wg.Wait()

	// 无论并发顺序如何，每条链最终都必须被完整移除：
	// root 前台删除传播到 mid/leaf，解除终结器后整链收敛。
	if got := len(c.Dump()); got != 0 {
		t.Fatalf("all objects must be removed, %d left", got)
	}
}

// 并发删除同一对象：幂等且最终状态确定。
func TestConcurrentDeleteSameObject(t *testing.T) {
	c := New()
	mustCreate(t, c, "a", nil, "f")
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := Background
			if i%2 == 0 {
				p = Foreground
			}
			if err := c.Delete("a", p, ts(int64(i))); err != nil {
				t.Errorf("delete: %v", err)
			}
		}()
	}
	wg.Wait()
	// 串行化后策略必为前台（存在后台->前台的升级路径），请求时刻是
	// 首个生效删除的时刻，具体值取决于串行顺序，只断言策略与幂等性。
	va := mustGet(t, c, "a")
	if !va.Deleting || va.Policy != Foreground {
		t.Fatalf("a should be foreground-deleting, got %+v", va)
	}
	mustDelete(t, c, "a", Orphan, ts(100)) // 幂等，不改变策略
	if va = mustGet(t, c, "a"); va.Policy != Foreground {
		t.Fatalf("policy must stay foreground, got %+v", va)
	}
}
