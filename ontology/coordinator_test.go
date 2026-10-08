package ontology

import (
	"fmt"
	"sort"
	"sync"
	"testing"
)

func allowAll() Rule  { return Rule{AllowRoles: []string{"*"}} }
func denyAll() Rule   { return Rule{} }
func onlyAdmin() Rule { return Rule{AllowRoles: []string{"admin"}} }

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustKind(t *testing.T, err error, kind ErrorKind) {
	t.Helper()
	e, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if e.Kind != kind {
		t.Fatalf("expected kind %s, got %s (%v)", kind, e.Kind, err)
	}
}

func dumpLog(t *testing.T, c *Coordinator) {
	t.Helper()
	for i, d := range c.Log().Entries() {
		t.Logf("decision[%d] op=%s input=%s output=%s basis=%s", i, d.Op, d.Input, d.Output, d.Basis)
	}
}

func writeOK(t *testing.T, c *Coordinator, obj string, expected int64, props map[string]any, role string) int64 {
	t.Helper()
	v, err := c.Write(obj, expected, props, role)
	if err != nil {
		t.Fatalf("write %s expected=%d: unexpected error: %v", obj, expected, err)
	}
	return v
}

// 覆盖后免疫：子类型显式覆盖某属性后，父类型后续任何变更都不影响该子类型及其下层。
func TestOverrideImmuneToParentChange(t *testing.T) {
	c := NewCoordinator()
	mustOK(t, c.CreateType("root", "", map[string]Rule{"secret": onlyAdmin()}, nil))
	mustOK(t, c.CreateType("mid", "root", nil, nil))
	mustOK(t, c.CreateType("leaf", "mid", nil, nil))
	// mid 显式放宽：所有人可写；leaf 未覆盖，继承 mid 的覆盖规则。
	mustOK(t, c.SetRule("mid", "secret", allowAll()))
	mustOK(t, c.CreateObject("o1", "leaf", map[string]any{"secret": "x"}))

	writeOK(t, c, "o1", 1, map[string]any{"secret": "y"}, "guest")

	// 父类型收紧为拒绝所有人：mid/leaf 已覆盖，完全免疫。
	mustOK(t, c.SetRule("root", "secret", denyAll()))
	inst, _ := c.Get("o1")
	writeOK(t, c, "o1", inst.Version, map[string]any{"secret": "z"}, "guest")

	// 对照：未覆盖的 plain 类型实时受父类型收紧影响。
	mustOK(t, c.CreateType("plain", "root", nil, nil))
	mustOK(t, c.CreateObject("o2", "plain", map[string]any{"secret": "x"}))
	_, err := c.Write("o2", 1, map[string]any{"secret": "y"}, "guest")
	mustKind(t, err, ErrPermissionDenied)
	dumpLog(t, c)
}

// 未覆盖子类型实时继承：父类型规则变更立即对未覆盖的子类型生效。
func TestLiveInheritanceRealtime(t *testing.T) {
	c := NewCoordinator()
	mustOK(t, c.CreateType("root", "", map[string]Rule{"field": allowAll()}, nil))
	mustOK(t, c.CreateType("child", "root", nil, nil))
	mustOK(t, c.CreateObject("o", "child", map[string]any{"field": 1}))

	writeOK(t, c, "o", 1, map[string]any{"field": 2}, "guest")

	// 父类型收紧，child 未覆盖，立即生效。
	mustOK(t, c.SetRule("root", "field", denyAll()))
	_, err := c.Write("o", 2, map[string]any{"field": 3}, "guest")
	mustKind(t, err, ErrPermissionDenied)

	// 父类型再放宽，同样立即生效。
	mustOK(t, c.SetRule("root", "field", allowAll()))
	writeOK(t, c, "o", 2, map[string]any{"field": 3}, "guest")
	dumpLog(t, c)
}

// 继承关系重组：未覆盖属性立即按新父链重新判定；已覆盖属性不受重组影响。
func TestReparentReResolves(t *testing.T) {
	c := NewCoordinator()
	mustOK(t, c.CreateType("rootA", "", map[string]Rule{"p": allowAll(), "q": allowAll()}, nil))
	mustOK(t, c.CreateType("rootB", "", map[string]Rule{"p": denyAll(), "q": denyAll()}, nil))
	// child 覆盖 q（放宽为所有人可写），p 未覆盖。
	mustOK(t, c.CreateType("child", "rootA", map[string]Rule{"q": allowAll()}, nil))
	mustOK(t, c.CreateObject("o", "child", map[string]any{"p": 1, "q": 1}))

	mustOK(t, c.Reparent("child", "rootB"))

	// p 未覆盖：立即按 rootB 拒绝。
	_, err := c.Write("o", 1, map[string]any{"p": 2}, "guest")
	mustKind(t, err, ErrPermissionDenied)
	// q 已覆盖：重组不影响，仍可写。
	writeOK(t, c, "o", 1, map[string]any{"q": 2}, "guest")

	// 重组回 rootA：p 恢复可写。
	mustOK(t, c.Reparent("child", "rootA"))
	writeOK(t, c, "o", 2, map[string]any{"p": 2}, "guest")

	// 成环的重组被拒绝，且不改变既有继承关系。
	mustOK(t, c.CreateType("grand", "child", nil, nil))
	err = c.Reparent("rootA", "grand")
	mustKind(t, err, ErrCycle)
	writeOK(t, c, "o", 3, map[string]any{"p": 3}, "guest")
	dumpLog(t, c)
}

// 判定顺序：版本冲突与权限不足同时成立时，必须先报版本冲突。
func TestVersionCheckedBeforePermission(t *testing.T) {
	c := NewCoordinator()
	mustOK(t, c.CreateType("root", "", nil, nil))
	mustOK(t, c.SetRule("root", "secret", denyAll()))
	mustOK(t, c.CreateObject("o", "root", map[string]any{"secret": "x"}))

	// 期望版本错误 + 权限必然不足：必须报版本冲突。
	_, err := c.Write("o", 99, map[string]any{"secret": "y"}, "guest")
	mustKind(t, err, ErrVersionConflict)

	// 版本正确但权限不足：报权限拒绝，二者可区分。
	_, err = c.Write("o", 1, map[string]any{"secret": "y"}, "guest")
	mustKind(t, err, ErrPermissionDenied)
	dumpLog(t, c)
}

// 拒绝优先级：对象不存在 > 版本冲突 > 权限拒绝 > 必需属性缺失。
func TestRejectionPriorityOrder(t *testing.T) {
	c := NewCoordinator()
	mustOK(t, c.CreateType("root", "", map[string]Rule{"a": denyAll()}, []string{"req"}))
	mustOK(t, c.CreateObject("o", "root", map[string]any{"req": 1, "a": 1}))

	// 1) 对象不存在优先于一切。
	_, err := c.Write("ghost", 99, map[string]any{"a": 1}, "guest")
	mustKind(t, err, ErrObjectNotFound)

	// 2) 版本冲突优先于权限与必需属性。
	_, err = c.Write("o", 99, map[string]any{"a": 1}, "guest")
	mustKind(t, err, ErrVersionConflict)

	// 3) 权限拒绝优先于必需属性缺失：版本正确、权限不足、且类型随后新增了必需属性。
	mustOK(t, c.SetRequired("root", []string{"req", "extra"}))
	_, err = c.Write("o", 1, map[string]any{"a": 2}, "guest")
	mustKind(t, err, ErrPermissionDenied)

	// 4) 权限通过后才是字段级细则：写入有权限的属性，但新必需属性 extra 缺失。
	mustOK(t, c.SetRule("root", "a", allowAll()))
	_, err = c.Write("o", 1, map[string]any{"a": 2}, "guest")
	mustKind(t, err, ErrMissingRequired)

	// 创建时必需属性缺失同样可区分。
	err = c.CreateObject("s2", "root", map[string]any{"a": 1})
	mustKind(t, err, ErrMissingRequired)
	dumpLog(t, c)
}

// 被拒绝的写入不产生任何副作用：版本号与对象状态保持不变。
func TestRejectedWriteNoSideEffects(t *testing.T) {
	c := NewCoordinator()
	mustOK(t, c.CreateType("root", "", map[string]Rule{"a": denyAll()}, nil))
	mustOK(t, c.CreateObject("o", "root", map[string]any{"a": 1}))

	before, _ := c.Get("o")
	_, err := c.Write("o", 99, map[string]any{"a": 2}, "admin")
	mustKind(t, err, ErrVersionConflict)
	_, err = c.Write("o", 1, map[string]any{"a": 2}, "guest")
	mustKind(t, err, ErrPermissionDenied)

	after, _ := c.Get("o")
	if after.Version != before.Version {
		t.Fatalf("rejected writes changed version: %d -> %d", before.Version, after.Version)
	}
	if after.Props["a"] != before.Props["a"] {
		t.Fatalf("rejected writes changed props: %v -> %v", before.Props, after.Props)
	}
	dumpLog(t, c)
}

// 并发写入：成功写入的版本号构成连续递增序列，可完整还原串行顺序。
func TestConcurrentWritesVersionSequence(t *testing.T) {
	c := NewCoordinator()
	mustOK(t, c.CreateType("root", "", map[string]Rule{"n": allowAll()}, nil))
	mustOK(t, c.CreateObject("o", "root", map[string]any{"n": 0}))

	const writers = 64
	results := make(chan int64, writers)
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for {
				inst, _ := c.Get("o")
				v, err := c.Write("o", inst.Version, map[string]any{"n": i}, "guest")
				if err == nil {
					results <- v
					return
				}
				if e, ok := err.(*Error); !ok || e.Kind != ErrVersionConflict {
					t.Errorf("unexpected error: %v", err)
					return
				}
			}
		}(i)
	}
	wg.Wait()
	close(results)

	got := []int64{}
	for v := range results {
		got = append(got, v)
	}
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
	if len(got) != writers {
		t.Fatalf("expected %d successful writes, got %d", writers, len(got))
	}
	for i, v := range got {
		if v != int64(i+2) { // 初始版本 1，每次成功写入严格 +1
			t.Fatalf("version sequence not contiguous at %d: got %d", i, v)
		}
	}
	inst, _ := c.Get("o")
	if inst.Version != int64(writers+1) {
		t.Fatalf("final version = %d, want %d", inst.Version, writers+1)
	}
	dumpLog(t, c)
}

// 规则变更 / 重组与写入并发交错：最终可观察结果须等价于某个串行顺序。
// 在单把互斥锁的实现下，只需验证无数据竞争、版本连续、状态始终一致。
func TestConcurrentRuleChangesAndWrites(t *testing.T) {
	c := NewCoordinator()
	mustOK(t, c.CreateType("rootA", "", map[string]Rule{"p": allowAll()}, nil))
	mustOK(t, c.CreateType("rootB", "", map[string]Rule{"p": denyAll()}, nil))
	mustOK(t, c.CreateType("child", "rootA", nil, nil))
	mustOK(t, c.CreateObject("o", "child", map[string]any{"p": 0}))

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				inst, _ := c.Get("o")
				_, _ = c.Write("o", inst.Version, map[string]any{"p": j}, "guest")
			}
		}(i)
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				_ = c.SetRule("rootA", "p", allowAll())
				_ = c.Reparent("child", "rootB")
				_ = c.Reparent("child", "rootA")
			}
		}(i)
	}
	wg.Wait()

	inst, _ := c.Get("o")
	writes := 0
	for _, d := range c.Log().Entries() {
		if d.Op == "Write" && d.Output == "ok" {
			writes++
		}
	}
	if inst.Version != int64(writes+1) {
		t.Fatalf("version %d inconsistent with %d successful writes", inst.Version, writes)
	}
	dumpLog(t, c)
}

// 性能可验证性：继承链查找访问的类型数等于链深，与类型总量及兄弟分支无关。
func TestResolveVisitsOnlyChainTypes(t *testing.T) {
	build := func(depth, siblings int) (*Coordinator, string) {
		c := NewCoordinator()
		mustOK(t, c.CreateType("root", "", map[string]Rule{"p": allowAll()}, nil))
		parent := "root"
		for i := 0; i < depth; i++ {
			id := fmt.Sprintf("chain-%d", i)
			mustOK(t, c.CreateType(id, parent, nil, nil))
			parent = id
		}
		for i := 0; i < siblings; i++ {
			mustOK(t, c.CreateType(fmt.Sprintf("sibling-%d", i), "root",
				map[string]Rule{"p": denyAll()}, nil))
		}
		return c, parent
	}

	const depth = 40
	c1, leaf1 := build(depth, 0)
	c2, leaf2 := build(depth, 5000)

	_, hit1, visited1 := c1.resolveRule(leaf1, "p")
	_, hit2, visited2 := c2.resolveRule(leaf2, "p")

	if len(visited1) != depth+1 || len(visited2) != depth+1 {
		t.Fatalf("visited %d / %d types, want chain depth %d", len(visited1), len(visited2), depth+1)
	}
	if hit1 != "root" || hit2 != "root" {
		t.Fatalf("rule should resolve at root, got %q / %q", hit1, hit2)
	}
	for _, id := range visited2 {
		if len(id) >= 7 && id[:7] == "sibling" {
			t.Fatalf("lookup traversed unrelated sibling branch %q", id)
		}
	}
	dumpLog(t, c2)
}
