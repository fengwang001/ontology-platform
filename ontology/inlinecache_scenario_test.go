package ontology

import (
	"reflect"
	"testing"
)

func TestInvalidLimit(t *testing.T) {
	for _, m := range []int{-1, 0, 1} {
		mgr, err := NewManager(m)
		if mgr != nil || ErrorCodeOf(err) != ErrInvalidLimit {
			t.Fatalf("NewManager(%d) = %v, %v, want ErrInvalidLimit", m, mgr, err)
		}
	}
	t.Logf("输入: NewManager(M=-1/0/1) | 输出: ErrInvalidLimit | 判定: M<2 整体拒绝")
}

func TestStateTransitionsAndInvalidation(t *testing.T) {
	const M = 3
	mgr, err := NewManager(M)
	if err != nil {
		t.Fatal(err)
	}

	check := func(site string, wantState State, wantEntries []Entry, want Stats) {
		t.Helper()
		snap, err := mgr.Snapshot(site)
		if err != nil {
			t.Fatalf("snapshot %s: %v", site, err)
		}
		if snap.State != wantState {
			t.Fatalf("site %s state = %s, want %s", site, snap.State, wantState)
		}
		if !reflect.DeepEqual(snap.Entries, wantEntries) {
			t.Fatalf("site %s entries = %#v, want %#v", site, snap.Entries, wantEntries)
		}
		if snap.Stats != want {
			t.Fatalf("site %s stats = %+v, want %+v", site, snap.Stats, want)
		}
	}

	for s := 1; s <= M+1; s++ {
		if err := mgr.Define(s, s*10); err != nil {
			t.Fatal(err)
		}
	}
	mustCreate := func(name string) {
		t.Helper()
		if err := mgr.CreateSite(name); err != nil {
			t.Fatal(err)
		}
	}
	mustAccess := func(site string, shape, wantTarget int) {
		t.Helper()
		got, err := mgr.Access(site, shape)
		if err != nil {
			t.Fatalf("access(%s,%d): %v", site, shape, err)
		}
		if got != wantTarget {
			t.Fatalf("access(%s,%d) = %v, want %d", site, shape, got, wantTarget)
		}
	}

	mustCreate("s")

	// 空 -> 单态：未命中并加入。
	mustAccess("s", 1, 10)
	t.Logf("输入: access(s,1) | 输出: 10 | 判定: 未命中, 条目数0->1, 状态 empty->monomorphic, misses=1")
	check("s", StateMonomorphic, []Entry{{1, 10}}, Stats{Misses: 1})

	// 单态命中。
	mustAccess("s", 1, 10)
	t.Logf("输入: access(s,1) | 输出: 10 | 判定: 命中缓存条目(shape=1), hits=1")
	check("s", StateMonomorphic, []Entry{{1, 10}}, Stats{Hits: 1, Misses: 1})

	// 单态 -> 多态。
	mustAccess("s", 2, 20)
	t.Logf("输入: access(s,2) | 输出: 20 | 判定: 未命中, 条目数1->2, 状态 monomorphic->polymorphic, misses=2")

	// 恰好 M 个形状仍为多态。
	mustAccess("s", 3, 30)
	t.Logf("输入: access(s,3) | 输出: 30 | 判定: 未命中, 条目数2->%d(=M), 仍为 polymorphic, misses=3", M)
	check("s", StatePolymorphic, []Entry{{1, 10}, {2, 20}, {3, 30}}, Stats{Hits: 1, Misses: 3})

	// 第 M+1 个形状：本次计未命中，清空缓存进入超多态。
	mustAccess("s", 4, 40)
	t.Logf("输入: access(s,4) | 输出: 40 | 判定: 未命中且加入前已有M个条目, 清空缓存并进入 megamorphic, misses=4")
	check("s", StateMegamorphic, nil, Stats{Hits: 1, Misses: 4})

	// 超多态访问直查方法表，只计 megaOps，不算命中/未命中。
	mustAccess("s", 4, 40)
	t.Logf("输入: access(s,4) | 输出: 40 | 判定: 已超多态, 直查全局方法表, megamorphic=1（不计命中/未命中）")
	check("s", StateMegamorphic, nil, Stats{Hits: 1, Misses: 4, Megamorphic: 1})

	// 超多态后重定义不回退：方法表更新，站点状态/条目/统计不变，访问拿到新目标。
	if err := mgr.Define(1, 100); err != nil {
		t.Fatal(err)
	}
	t.Logf("输入: define(1,100) | 输出: ok(重定义) | 判定: s 已超多态, 不删条目也不回退, 统计不变")
	check("s", StateMegamorphic, nil, Stats{Hits: 1, Misses: 4, Megamorphic: 1})
	mustAccess("s", 1, 100)
	t.Logf("输入: access(s,1) | 输出: 100 | 判定: 超多态直查方法表拿到新目标, megamorphic=2")
	check("s", StateMegamorphic, nil, Stats{Hits: 1, Misses: 4, Megamorphic: 2})

	// 重定义使另一站点 多态 -> 单态 -> 空。
	mustCreate("t")
	mustAccess("t", 1, 100)
	mustAccess("t", 2, 20)
	mustAccess("t", 3, 30)
	check("t", StatePolymorphic, []Entry{{1, 100}, {2, 20}, {3, 30}}, Stats{Misses: 3})
	if err := mgr.Define(3, 300); err != nil {
		t.Fatal(err)
	}
	t.Logf("输入: define(3,300) | 输出: ok(重定义) | 判定: t 缓存了shape3, 删除该条目, 3->2 仍 polymorphic")
	check("t", StatePolymorphic, []Entry{{1, 100}, {2, 20}}, Stats{Misses: 3})
	if err := mgr.Define(2, 200); err != nil {
		t.Fatal(err)
	}
	t.Logf("输入: define(2,200) | 输出: ok(重定义) | 判定: t 删除shape2条目, 2->1, polymorphic->monomorphic")
	check("t", StateMonomorphic, []Entry{{1, 100}}, Stats{Misses: 3})
	if err := mgr.Define(1, 1000); err != nil {
		t.Fatal(err)
	}
	t.Logf("输入: define(1,1000) | 输出: ok(重定义) | 判定: t 删除shape1条目, 1->0, monomorphic->empty")
	check("t", StateEmpty, nil, Stats{Misses: 3})

	// 相同目标重定义不失效。
	mustCreate("u")
	mustAccess("u", 1, 1000)
	mustAccess("u", 2, 200)
	check("u", StatePolymorphic, []Entry{{1, 1000}, {2, 200}}, Stats{Misses: 2})
	if err := mgr.Define(1, 1000); err != nil {
		t.Fatal(err)
	}
	t.Logf("输入: define(1,1000) | 输出: ok(同目标) | 判定: 目标相同, 无任何影响, u 条目与统计不变")
	check("u", StatePolymorphic, []Entry{{1, 1000}, {2, 200}}, Stats{Misses: 2})

	// 未定义形状的访问被拒绝且不改变站点与计数。
	mustCreate("v")
	if _, accessErr := mgr.Access("v", 99); ErrorCodeOf(accessErr) != ErrShapeUndefined {
		t.Fatalf("access undefined shape: err = %v, want ErrShapeUndefined", accessErr)
	}
	t.Logf("输入: access(v,99) | 输出: ErrShapeUndefined | 判定: 形状99未定义, 整体拒绝, 站点与计数不变")
	check("v", StateEmpty, nil, Stats{})
	if err := mgr.Define(99, 990); err != nil {
		t.Fatal(err)
	}
	mustAccess("v", 99, 990)
	t.Logf("输入: define(99,990) 后 access(v,99) | 输出: 990 | 判定: 定义后未命中加入, 证明拒绝访问未提前改变站点")
	check("v", StateMonomorphic, []Entry{{99, 990}}, Stats{Misses: 1})

	// 其它可区分拒绝原因。
	if err := mgr.CreateSite("v"); ErrorCodeOf(err) != ErrSiteExists {
		t.Fatalf("duplicate create: %v", err)
	}
	if _, err := mgr.Access("nope", 1); ErrorCodeOf(err) != ErrSiteNotFound {
		t.Fatalf("missing site: %v", err)
	}
	if _, err := mgr.Access("v", 0); ErrorCodeOf(err) != ErrInvalidShape {
		t.Fatalf("invalid shape access: %v", err)
	}
	if err := mgr.Define(-3, 1); ErrorCodeOf(err) != ErrInvalidShape {
		t.Fatalf("invalid shape define: %v", err)
	}
	t.Logf("输入: 重复create/不存在站点/非正整数形状 | 输出: site_exists/site_not_found/invalid_shape | 判定: 原因可区分")

	// 每站点 命中+未命中+超多态 == 成功访问总次数。
	for _, name := range mgr.Sites() {
		snap, _ := mgr.Snapshot(name)
		// 本测试中每个站点的成功访问都计入某一口径。
		if snap.Stats.Total() <= 0 {
			t.Fatalf("site %s expected positive successful accesses", name)
		}
		t.Logf("站点 %s: hits=%d misses=%d mega=%d total=%d 状态=%s",
			name, snap.Stats.Hits, snap.Stats.Misses, snap.Stats.Megamorphic, snap.Stats.Total(), snap.State)
	}
}
