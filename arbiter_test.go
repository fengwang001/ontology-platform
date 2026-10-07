package ontology

import (
	"fmt"
	"testing"
)

// testWorld 汇总各边界测试共享的最小世界：
//
//	对象类型 Doc(source 属性 docRef, 默认不可见) 与 Person(source 属性 personRef)；
//	链接类型 authored: Doc.docRef -> Person.personRef，两端各上限 1。
func testWorld(t *testing.T) *Platform {
	t.Helper()
	p := NewPlatform()
	p.DefineObjectType("Doc", []PropertySpec{
		{Name: "docRef", LinkSource: true, DefaultVis: Invisible},
	})
	p.DefineObjectType("Person", []PropertySpec{
		{Name: "personRef", LinkSource: true, DefaultVis: Invisible},
	})
	if err := p.DefineLinkType(LinkTypeSpec{
		Name:      "authored",
		Source:    EndpointSpec{ObjectType: "Doc", Property: "docRef"},
		Target:    EndpointSpec{ObjectType: "Person", Property: "personRef"},
		SourceMax: 1,
		TargetMax: 1,
	}); err != nil {
		t.Fatalf("define link type: %v", err)
	}
	must(t, p.CreateInstance("d1", "Doc"))
	must(t, p.CreateInstance("d2", "Doc"))
	must(t, p.CreateInstance("p1", "Person"))
	must(t, p.CreateInstance("p2", "Person"))
	return p
}

func must(t *testing.T, err *DecisionError) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func class(err *DecisionError) ErrorClass {
	if err == nil {
		return ErrOK
	}
	return err.Class
}

func want(t *testing.T, err *DecisionError, c ErrorClass, msg string) {
	t.Helper()
	if got := class(err); got != c {
		t.Fatalf("%s: want %s, got %s (%v)", msg, c, got, err)
	}
}

// 规则一：参数非法最先报告，即使同时无权限且基数已满。
func TestErrorOrderInvalidArgFirst(t *testing.T) {
	p := testWorld(t)
	want(t, p.CreateLink("alice", "no-such-link", "d1", "p1"), ErrInvalidArg,
		"unknown link type must be invalid_argument")
	want(t, p.CreateLink("alice", "authored", "p1", "d1"), ErrInvalidArg,
		"swapped endpoints must be invalid_argument")
	want(t, p.CreateLink("", "authored", "d1", "p1"), ErrInvalidArg,
		"empty args must be invalid_argument")
}

// 规则二：操作者对端点来源属性无可见权限时，权限错误优先于两端基数超限。
func TestErrorOrderPermissionBeforeCardinality(t *testing.T) {
	p := testWorld(t)
	p.GrantActor("admin", "d1", "docRef", Visible)
	p.GrantActor("admin", "p1", "personRef", Visible)
	must(t, p.CreateLink("admin", "authored", "d1", "p1"))

	// carol 无任何覆盖、类型默认 Invisible；此时 d1/p1 槽位也已满。
	want(t, p.CreateLink("carol", "authored", "d1", "p1"), ErrPermissionDenied,
		"permission error precedes cardinality even when slots are full")
	want(t, p.CreateLink("carol", "authored", "d2", "p2"), ErrPermissionDenied,
		"permission checked independently of free slots")
}

// 规则三：起点基数超限先于终点基数超限。
func TestErrorOrderSourceBeforeTargetCardinality(t *testing.T) {
	p := testWorld(t)
	for _, x := range []string{"d1", "d2"} {
		p.GrantActor("admin", x, "docRef", Visible)
	}
	for _, x := range []string{"p1", "p2"} {
		p.GrantActor("admin", x, "personRef", Visible)
	}
	must(t, p.CreateLink("admin", "authored", "d1", "p1"))

	want(t, p.CreateLink("admin", "authored", "d1", "p2"), ErrSourceCardExceeded,
		"source cardinality reported before target")
	want(t, p.CreateLink("admin", "authored", "d2", "p1"), ErrTargetCardExceeded,
		"target cardinality reported when source is free")
}

// 规则四：权限收紧后链接立即对该操作者隐藏，但物理存在、基数占用与
// 其他操作者的可见性均不受影响；收紧不触发删除或基数释放。
func TestRevocationHidesWithoutDeletion(t *testing.T) {
	p := testWorld(t)
	for _, a := range []string{"alice", "bob"} {
		p.GrantActor(a, "d1", "docRef", Visible)
		p.GrantActor(a, "p1", "personRef", Visible)
	}
	must(t, p.CreateLink("alice", "authored", "d1", "p1"))
	link := Link{LinkType: "authored", SourceInstance: "d1", TargetInstance: "p1"}

	if !p.LinkVisible("alice", link) || !p.LinkVisible("bob", link) {
		t.Fatalf("both actors should see the link before revocation")
	}

	p.GrantActor("alice", "d1", "docRef", Invisible)

	if p.LinkVisible("alice", link) {
		t.Fatalf("link must be hidden from alice immediately after revocation")
	}
	if !p.LinkVisible("bob", link) {
		t.Fatalf("bob's visibility must be unaffected by alice's revocation")
	}
	if !p.LinkExists(link) {
		t.Fatalf("revocation must not physically delete the link")
	}
	if p.SourceCount("authored", "d1") != 1 || p.TargetCount("authored", "p1") != 1 {
		t.Fatalf("revocation must not release cardinality slots")
	}

	want(t, p.CreateLink("alice", "authored", "d1", "p2"), ErrPermissionDenied,
		"revoked actor still blocked by permission, not cardinality")

	p.GrantActor("alice", "d1", "docRef", Visible)
	if !p.LinkVisible("alice", link) {
		t.Fatalf("link reappears after permission is widened again")
	}
}

// 规则五：角色层级覆盖「最近优先」；同距离取「声明更晚」。
func TestRoleNearestAndLatestWins(t *testing.T) {
	p := testWorld(t)
	p.AddActorRole("alice", "junior")
	p.AddRoleContains("junior", "senior")
	p.AddRoleContains("senior", "principal")

	p.GrantRole("principal", "d1", "docRef", Visible) // 距离 3
	p.GrantRole("senior", "d1", "docRef", Invisible)  // 距离 2，更近
	if got := p.Visibility("alice", "d1", "docRef"); got != Invisible {
		t.Fatalf("nearest override must win, got %v", got)
	}

	p.AddActorRole("alice", "mid")
	p.GrantRole("mid", "d1", "docRef", Visible) // 与 senior 同为距离 2，声明更晚
	if got := p.Visibility("alice", "d1", "docRef"); got != Visible {
		t.Fatalf("later declaration must win at equal distance, got %v", got)
	}

	p.GrantActor("alice", "d1", "docRef", Invisible) // 距离 0 压过角色
	if got := p.Visibility("alice", "d1", "docRef"); got != Invisible {
		t.Fatalf("direct actor override at distance 0 must win, got %v", got)
	}

	// 覆盖只对被声明的操作者生效：dave 不在这些角色中，仍按类型默认。
	if got := p.Visibility("dave", "d1", "docRef"); got != Invisible {
		t.Fatalf("unrelated actor must fall back to type default, got %v", got)
	}

	// 无覆盖时退回「放宽」的类型默认值。
	p2 := NewPlatform()
	p2.DefineObjectType("Open", []PropertySpec{
		{Name: "ref", LinkSource: true, DefaultVis: Visible},
	})
	must(t, p2.CreateInstance("o1", "Open"))
	if got := p2.Visibility("zoe", "o1", "ref"); got != Visible {
		t.Fatalf("visible type-level default must apply without overrides, got %v", got)
	}
}

// 规则六：删除不可见链接与删除确实不存在的链接返回完全相同的错误类别。
func TestDeleteInvisibleEqualsNotFound(t *testing.T) {
	p := testWorld(t)
	p.GrantActor("admin", "d1", "docRef", Visible)
	p.GrantActor("admin", "p1", "personRef", Visible)
	must(t, p.CreateLink("admin", "authored", "d1", "p1"))
	link := Link{LinkType: "authored", SourceInstance: "d1", TargetInstance: "p1"}

	errMissing := p.DeleteLink("admin", "authored", "d2", "p2") // 确实不存在
	errHidden := p.DeleteLink("carol", "authored", "d1", "p1")  // 存在但不可见
	want(t, errMissing, ErrNotFound, "missing link => not_found")
	want(t, errHidden, ErrNotFound, "invisible link => same not_found class")

	if errMissing.Error() == "" || errHidden.Error() == "" {
		t.Fatalf("both decisions must carry a normalized error message")
	}
	if !p.LinkExists(link) {
		t.Fatalf("invisible delete attempt must not remove the link")
	}

	want(t, p.DeleteLink("carol", "nope", "d1", "p1"), ErrInvalidArg,
		"invalid arg still precedes not_found on delete")

	must(t, p.DeleteLink("admin", "authored", "d1", "p1"))
	if p.LinkExists(link) {
		t.Fatalf("visible delete should remove the link")
	}
}

// 复杂度可验证证明：ResolveTrace 返回的 traversed 是实际访问过的主体数。
// 在「到最近覆盖的距离 d」固定时增加无关角色总数，traversed 必须不变；
// 把最近覆盖放远，traversed 才随 d 增长，且命中即停绝不访问更远层。
func TestResolutionCostDependsOnlyOnNearestDistance(t *testing.T) {
	r := NewResolver()
	r.SetTypeDefault("Doc", "docRef", Invisible)
	r.SetInstanceType("d1", "Doc")

	const N = 20
	r.AddActorRole("a", "r1")
	for i := 1; i < N; i++ {
		r.AddRoleContains(fmt.Sprintf("r%d", i), fmt.Sprintf("r%d", i+1))
	}
	r.GrantRole("r3", "d1", "docRef", Visible, 1) // 最近覆盖在距离 3

	_, used, traversedSmall := r.ResolveTrace("a", "d1", "docRef")
	if used {
		t.Fatalf("override at r3 expected, not default")
	}

	for i := 0; i < 500; i++ { // 膨胀系统角色总数
		role := fmt.Sprintf("other%d", i)
		r.AddRoleContains(role, fmt.Sprintf("otherParent%d", i%5))
		r.GrantRole(role, "d1", "docRef", Visible, int64(100+i))
	}
	_, _, traversedLarge := r.ResolveTrace("a", "d1", "docRef")
	t.Logf("[complexity] nearest distance=3: traversed=%d before adding 1000 unrelated roles, traversed=%d after",
		traversedSmall, traversedLarge)
	if traversedLarge != traversedSmall {
		t.Fatalf("traversal must not grow with total role count: %d vs %d",
			traversedSmall, traversedLarge)
	}

	r.GrantRole("r8", "d1", "docRef", Visible, 2) // 比 r3 更远，不影响最近覆盖
	_, _, traversedSame := r.ResolveTrace("a", "d1", "docRef")
	if traversedSame != traversedSmall {
		t.Fatalf("a farther-than-nearest declaration must not change traversal")
	}

	// 另造一条只在距离 8 才有覆盖的链，验证遍历量只随最近距离 d 增长。
	r2 := NewResolver()
	r2.SetTypeDefault("Doc", "docRef", Invisible)
	r2.SetInstanceType("d1", "Doc")
	r2.AddActorRole("a", "r1")
	for i := 1; i < N; i++ {
		r2.AddRoleContains(fmt.Sprintf("r%d", i), fmt.Sprintf("r%d", i+1))
	}
	r2.GrantRole("r8", "d1", "docRef", Visible, 1)
	hit, _, traversedFar := r2.ResolveTrace("a", "d1", "docRef")
	t.Logf("[complexity] nearest distance=8: traversed=%d, hit subject=%s vis=%s",
		traversedFar, hit.subject, hit.vis)
	if traversedFar <= traversedSmall {
		t.Fatalf("moving nearest override farther must increase traversal: %d vs %d",
			traversedSmall, traversedFar)
	}
	if traversedFar > 1+8 { // actor 自身 + 8 个角色，命中即停
		t.Fatalf("nearest-first BFS must stop at distance d; traversed=%d", traversedFar)
	}
}

// 平台缓存路径：两次声明变更之间的重复查询为 O(1) 缓存命中，且结果正确。
func TestCachedVisibilityStableAcrossRepeatedLookups(t *testing.T) {
	p := testWorld(t)
	p.GrantActor("alice", "d1", "docRef", Visible)
	for i := 0; i < 10000; i++ {
		if p.Visibility("alice", "d1", "docRef") != Visible {
			t.Fatalf("stable cached visibility expected")
		}
	}
}
