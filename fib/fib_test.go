package fib

import (
	"errors"
	"sync"
	"testing"

	"ontology/nhgroup"
)

func mustCreate(t *testing.T, f *FIB, gid, n, ti, tu int, members []nhgroup.Spec, now int64) {
	t.Helper()
	if err := f.CreateGroup(gid, n, ti, tu, members, now); err != nil {
		t.Fatalf("CreateGroup(%d): %v", gid, err)
	}
}

// TestLastMember 唯一成员（含失效）报最后成员。
func TestLastMember(t *testing.T) {
	f := New()
	mustCreate(t, f, 1, 4, 0, 0, []nhgroup.Spec{{NH: 5, Weight: 1}}, 0)
	if err := f.NexthopDown(5, 10); err != nil {
		t.Fatal(err)
	}
	if err := f.RemoveMember(1, 5, 11); !errors.Is(err, ErrLastMember) {
		t.Fatalf("唯一失效成员移除 want ErrLastMember, got %v", err)
	}
}

// TestDownUp 失效立即指派，恢复时空桶回填、其余惰性。
func TestDownUp(t *testing.T) {
	f := New()
	mustCreate(t, f, 1, 8, 10, 50, []nhgroup.Spec{{NH: 1, Weight: 1}, {NH: 2, Weight: 1}}, 0)
	if err := f.NexthopDown(2, 100); err != nil {
		t.Fatal(err)
	}
	if got := bucketsOf(t, f, 1); !sameInts(got, []int{1, 1, 1, 1, 1, 1, 1, 1}) {
		t.Fatalf("Down 立即指派=%v", got)
	}
	if alive, _ := f.Alive(1, 2); alive {
		t.Fatalf("成员资格保留但应失效")
	}
	if err := f.NexthopUp(2, 200); err != nil {
		t.Fatal(err)
	}
	if got := bucketsOf(t, f, 1); !sameInts(got, []int{1, 1, 1, 1, 1, 1, 1, 1}) {
		t.Fatalf("Up 当次不搬存活成员的桶, got %v", got)
	}
	if _, err := f.Lookup(1, 0, 210); err != nil {
		t.Fatal(err)
	}
	got := bucketsOf(t, f, 1)
	count := map[int]int{}
	for _, o := range got {
		count[o]++
	}
	if count[1] != 4 || count[2] != 4 {
		t.Fatalf("Up 后惰性应到 4/4, got %v", got)
	}
}

// TestDownAllEmpties 全失效置空；空 Lookup 报无下一跳；重复 Down 空操作。
func TestDownAllEmpties(t *testing.T) {
	f := New()
	mustCreate(t, f, 1, 4, 0, 0, []nhgroup.Spec{{NH: 1, Weight: 1}}, 0)
	if err := f.NexthopDown(1, 5); err != nil {
		t.Fatal(err)
	}
	if got := bucketsOf(t, f, 1); !sameInts(got, []int{0, 0, 0, 0}) {
		t.Fatalf("全失效应置空, got %v", got)
	}
	if _, err := f.Lookup(1, 0, 6); !errors.Is(err, ErrNoNexthop) {
		t.Fatalf("空桶 Lookup want ErrNoNexthop, got %v", err)
	}
	if err := f.NexthopDown(1, 7); err != nil {
		t.Fatalf("重复 Down 应空操作成功, got %v", err)
	}
}

// TestRouteFallback LPM、全死回退与两类落空错误区分。
func TestRouteFallback(t *testing.T) {
	f := New()
	mustCreate(t, f, 1, 8, 0, 0, []nhgroup.Spec{{NH: 1, Weight: 1}}, 0)
	mustCreate(t, f, 2, 8, 0, 0, []nhgroup.Spec{{NH: 2, Weight: 1}}, 0)
	pA := Prefix{Addr: 0x0a000000, Len: 8}
	pB := Prefix{Addr: 0x0a010000, Len: 16}
	if err := f.AddRoute(pA, 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := f.AddRoute(pB, 2, 2); err != nil {
		t.Fatal(err)
	}
	if nh, err := f.Route(0x0a010203, 0, 3); err != nil || nh != 2 {
		t.Fatalf("最长前缀应到组b成员2, got %d,%v", nh, err)
	}
	if err := f.NexthopDown(2, 4); err != nil {
		t.Fatal(err)
	}
	if nh, err := f.Route(0x0a010203, 0, 5); err != nil || nh != 1 {
		t.Fatalf("b全死回退组a成员1, got %d,%v", nh, err)
	}
	if err := f.NexthopDown(1, 6); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Route(0x0a010203, 0, 7); !errors.Is(err, ErrNoNexthop) {
		t.Fatalf("有前缀但全死 want ErrNoNexthop, got %v", err)
	}
	if _, err := f.Route(0x0b000001, 0, 8); !errors.Is(err, ErrNoRoute) {
		t.Fatalf("无覆盖 want ErrNoRoute, got %v", err)
	}
}

// TestDeleteGroupInUse 引用时报使用中。
func TestDeleteGroupInUse(t *testing.T) {
	f := New()
	mustCreate(t, f, 1, 4, 0, 0, []nhgroup.Spec{{NH: 1, Weight: 1}}, 0)
	if err := f.AddRoute(Prefix{Len: 0}, 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := f.DeleteGroup(1, 2); !errors.Is(err, ErrInUse) {
		t.Fatalf("使用中 want ErrInUse, got %v", err)
	}
	if err := f.DelRoute(Prefix{Len: 0}, 3); err != nil {
		t.Fatal(err)
	}
	if err := f.DeleteGroup(1, 4); err != nil {
		t.Fatalf("无引用后应可删除, got %v", err)
	}
	if _, err := f.Lookup(1, 0, 5); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除后 want NotFound, got %v", err)
	}
}

// TestTouched 平衡时恰为 1（N=64 与 4096 两档）。
func TestTouched(t *testing.T) {
	for _, n := range []int{64, 4096} {
		f := New()
		mustCreate(t, f, 1, n, 0, 0, []nhgroup.Spec{{NH: 1, Weight: 1}, {NH: 2, Weight: 1}}, 0)
		if _, err := f.Lookup(1, uint64(n-1), 10); err != nil {
			t.Fatal(err)
		}
		if got := f.LastTouched(); got != 1 {
			t.Fatalf("N=%d 平衡组 touched=%d want 1", n, got)
		}
	}
}

// TestTouchedUnbalancedBound 不平衡组 N 之和 + 1 的上界对照。
func TestTouchedUnbalancedBound(t *testing.T) {
	f := New()
	mustCreate(t, f, 1, 64, 1_000_000, 0, []nhgroup.Spec{{NH: 1, Weight: 1}}, 0)
	mustCreate(t, f, 2, 4096, 1_000_000, 0, []nhgroup.Spec{{NH: 1, Weight: 1}}, 1)
	mustCreate(t, f, 3, 8, 0, 0, []nhgroup.Spec{{NH: 1, Weight: 1}}, 2)
	if err := f.AddMember(1, 2, 1, 10); err != nil {
		t.Fatal(err)
	}
	if err := f.AddMember(2, 2, 1, 11); err != nil {
		t.Fatal(err)
	}
	// 下一操作开头整理两个不平衡组：大 Ti 使桶均不搬，但每桶仍被扫描触碰。
	if _, err := f.Lookup(3, 0, 12); err != nil {
		t.Fatal(err)
	}
	if got, bound := f.LastTouched(), 64+4096+1; got != bound {
		t.Fatalf("不平衡 touched=%d want 恰为 %d（各不平衡组 N 之和 + 1）", got, bound)
	}
	// 组 1、2 仍不平衡：第二次查询仍不超过 4161。
	if _, err := f.Lookup(3, 0, 13); err != nil {
		t.Fatal(err)
	}
	if got := f.LastTouched(); got > 64+4096+1 {
		t.Fatalf("不平衡 touched=%d 超过上界 %d", got, 64+4096+1)
	}
	// 移除新成员后立即指派，两组恢复平衡，下一次查询恰触碰 1。
	if err := f.RemoveMember(1, 2, 14); err != nil {
		t.Fatal(err)
	}
	if err := f.RemoveMember(2, 2, 15); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Lookup(3, 0, 16); err != nil {
		t.Fatal(err)
	}
	if got := f.LastTouched(); got != 1 {
		t.Fatalf("全部平衡后 touched=%d want 1", got)
	}
}

// TestRejectOrder 校验拒绝次序；被拒操作不推进时钟。
func TestRejectOrder(t *testing.T) {
	f := New()
	mustCreate(t, f, 1, 8, 0, 0, []nhgroup.Spec{{NH: 1, Weight: 1}}, 0)
	if _, err := f.Lookup(1, 0, 5); err != nil {
		t.Fatal(err)
	}
	// 非法参数优先于时钟回退。
	if err := f.CreateGroup(2, 0, 0, 0, []nhgroup.Spec{{NH: 1, Weight: 1}}, -1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("非法优先, got %v", err)
	}
	// 时钟回退优先于对象不存在。
	if err := f.AddMember(99, 3, 1, 3); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("时钟优先, got %v", err)
	}
	// 不存在优先于已存在/最后成员：不存在的组。
	if err := f.RemoveMember(99, 1, 6); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在优先, got %v", err)
	}
	// 已存在优先于最后成员。
	if err := f.AddMember(1, 1, 1, 7); !errors.Is(err, ErrExists) {
		t.Fatalf("已存在优先, got %v", err)
	}
	// 未被任何组引用的下一跳 Down -> NotFound。
	if err := f.NexthopDown(777, 8); !errors.Is(err, ErrNotFound) {
		t.Fatalf("未引用 nh want NotFound, got %v", err)
	}
	// 主机位非零非法。
	if err := f.AddRoute(Prefix{Addr: 0x0a000001, Len: 8}, 1, 9); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("主机位非零 want Invalid, got %v", err)
	}
	// 被拒不推进时钟：非法 AddRoute 被拒，now=10 仍可接受。
	if _, err := f.Lookup(1, 0, 10); err != nil {
		t.Fatalf("被拒操作不应推进时钟, now=10 应可接受, got %v", err)
	}
}

// TestConcurrentSafe 并发压力，配合 -race 检测数据竞争。
func TestConcurrentSafe(t *testing.T) {
	f := New()
	mustCreate(t, f, 1, 64, 5, 100, []nhgroup.Spec{{NH: 1, Weight: 1}, {NH: 2, Weight: 1}}, 0)
	var wg sync.WaitGroup
	var clock sync.Mutex
	now := int64(1)
	next := func() int64 {
		clock.Lock()
		defer clock.Unlock()
		n := now
		now++
		return n
	}
	run := func(fn func(int64) error) {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			_ = fn(next())
		}
	}
	wg.Add(4)
	go run(func(n int64) error { _, e := f.Lookup(1, uint64(n), n); return e })
	go run(func(n int64) error { return f.SetWeight(1, 1, 1+int(n%3)+1, n) })
	go run(func(n int64) error {
		if n%2 == 0 {
			return f.NexthopDown(2, n)
		}
		return f.NexthopUp(2, n)
	})
	go run(func(n int64) error { _, e := f.Route(0, uint64(n), n); return e })
	wg.Wait()
}

func bucketsOf(t *testing.T, f *FIB, gid int) []int {
	t.Helper()
	b, ok := f.GroupBuckets(gid)
	if !ok {
		t.Fatalf("组 %d 不存在", gid)
	}
	return b
}

func sameInts(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestWorkedExample 复现题目完整示例。
func TestWorkedExample(t *testing.T) {
	f := New()
	mustCreate(t, f, 1, 8, 10, 50, []nhgroup.Spec{{NH: 1, Weight: 1}, {NH: 2, Weight: 1}}, 0)
	if got := bucketsOf(t, f, 1); !sameInts(got, []int{1, 2, 1, 2, 1, 2, 1, 2}) {
		t.Fatalf("建组=%v", got)
	}
	if nh, err := f.Lookup(1, 0, 95); err != nil || nh != 1 {
		t.Fatalf("Lookup(0)@95=%d,%v 依据: 桶0主人1", nh, err)
	}
	if nh, err := f.Lookup(1, 1, 95); err != nil || nh != 2 {
		t.Fatalf("Lookup(1)@95=%d,%v", nh, err)
	}
	if err := f.AddMember(1, 3, 2, 100); err != nil {
		t.Fatal(err)
	}
	if got := bucketsOf(t, f, 1); !sameInts(got, []int{1, 2, 1, 2, 1, 2, 1, 2}) {
		t.Fatalf("AddMember 当次不应搬桶, got %v", got)
	}
	if since, unbal, _ := f.GroupUnbalancedSince(1); !unbal || since != 100 {
		t.Fatalf("unbalancedSince 应为 100, got %d,%v", since, unbal)
	}
	nh, err := f.Lookup(1, 0, 101)
	if err != nil || nh != 1 {
		t.Fatalf("Lookup@101=%d,%v 依据: 桶0热点距用6<Ti", nh, err)
	}
	want := []int{1, 2, 3, 3, 3, 3, 1, 2}
	if got := bucketsOf(t, f, 1); !sameInts(got, want) {
		t.Fatalf("整理后=%v want %v", got, want)
	}
}

// TestForceAtExactTu 频繁使用下恰满 Tu 强制。
func TestForceAtExactTu(t *testing.T) {
	f := New()
	mustCreate(t, f, 1, 8, 10, 50, []nhgroup.Spec{{NH: 1, Weight: 1}, {NH: 2, Weight: 1}}, 0)
	for h := uint64(0); h < 8; h++ {
		if _, err := f.Lookup(1, h, 99); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.AddMember(1, 3, 2, 100); err != nil {
		t.Fatal(err)
	}
	for now := int64(101); now <= 149; now++ {
		for h := uint64(0); h < 8; h++ {
			if _, err := f.Lookup(1, h, now); err != nil {
				t.Fatal(err)
			}
		}
	}
	if got := bucketsOf(t, f, 1); !sameInts(got, []int{1, 2, 1, 2, 1, 2, 1, 2}) {
		t.Fatalf("热点期间不应搬桶, got %v", got)
	}
	if _, err := f.Lookup(1, 0, 150); err != nil {
		t.Fatal(err)
	}
	want := []int{3, 3, 3, 3, 1, 2, 1, 2}
	if got := bucketsOf(t, f, 1); !sameInts(got, want) {
		t.Fatalf("恰满 Tu 强制: got %v want %v", got, want)
	}
}

// TestUnbalancedSinceNotReset 不平衡期间再变更不重置 since。
func TestUnbalancedSinceNotReset(t *testing.T) {
	f := New()
	mustCreate(t, f, 1, 8, 1_000_000, 0, []nhgroup.Spec{{NH: 1, Weight: 1}}, 0)
	if err := f.AddMember(1, 2, 1, 100); err != nil {
		t.Fatal(err)
	}
	if err := f.SetWeight(1, 2, 5, 120); err != nil {
		t.Fatal(err)
	}
	if err := f.AddMember(1, 3, 1, 130); err != nil {
		t.Fatal(err)
	}
	since, unbal, _ := f.GroupUnbalancedSince(1)
	if !unbal || since != 100 {
		t.Fatalf("since 不应重置, got %d,%v 依据: 仅平衡->不平衡设置", since, unbal)
	}
}

// TestRemoveMemberImmediate 移除后立即指派回到 4/4。
func TestRemoveMemberImmediate(t *testing.T) {
	f := New()
	mustCreate(t, f, 1, 8, 10, 50, []nhgroup.Spec{{NH: 1, Weight: 1}, {NH: 2, Weight: 1}}, 0)
	if err := f.AddMember(1, 3, 2, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Lookup(1, 0, 101); err != nil {
		t.Fatal(err)
	}
	if err := f.RemoveMember(1, 3, 200); err != nil {
		t.Fatal(err)
	}
	want := []int{1, 2, 1, 2, 1, 2, 1, 2}
	if got := bucketsOf(t, f, 1); !sameInts(got, want) {
		t.Fatalf("RemoveMember 立即指派: got %v want %v", got, want)
	}
	if _, unbal, _ := f.GroupUnbalancedSince(1); unbal {
		t.Fatalf("移除后应恢复平衡")
	}
}
