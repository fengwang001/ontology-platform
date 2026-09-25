package access

import (
	"fmt"
	"testing"

	"ontology/acl"
)

// TestAllowedMatrix 遍历 主体×属性×动作×默认策略 的判定矩阵。
func TestAllowedMatrix(t *testing.T) {
	pol := acl.NewPolicy()
	u := acl.User("u")
	g1, g2 := acl.Group("g1"), acl.Group("g2")
	pol.AddMember(g1, u)
	pol.AddMember(g2, g1) // 嵌套：u ∈ g1 ∈ g2
	pol.Grant(u, "a", acl.Read)
	pol.Grant(g1, "b", acl.Write)
	pol.Grant(g2, "c", acl.Read)
	pol.Grant(g2, "c", acl.Write)
	pol.Revoke(g2, "c", acl.Write) // 撤销后不再生效

	stranger := acl.User("stranger")
	cases := []struct {
		subj acl.Subject
		prop string
		act  acl.Action
		want bool
	}{
		{u, "a", acl.Read, true},     // 直接授权
		{u, "a", acl.Write, false},   // 动作不匹配，默认拒绝
		{u, "b", acl.Write, true},    // 组继承
		{u, "b", acl.Read, false},    // 组授权不越界
		{u, "c", acl.Read, true},     // 嵌套组继承
		{u, "c", acl.Write, false},   // 组授权已撤销
		{u, "zzz", acl.Read, false},  // 无授权属性，默认拒绝
		{u, "zzz", acl.Write, false}, // 无授权属性，默认拒绝
		{stranger, "a", acl.Read, false},
		{stranger, "a", acl.Write, false},
		{stranger, "b", acl.Write, false},
		{stranger, "c", acl.Read, false},
	}
	for _, tc := range cases {
		name := fmt.Sprintf("%s/%s/%s", tc.subj, tc.prop, tc.act)
		if got := NewEvaluator(pol).Allowed(tc.subj, tc.prop, tc.act); got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}

// TestDirectGrantUnionWithGroup 直接授权与组授权并集生效，直接授权优先可判定。
func TestDirectGrantUnionWithGroup(t *testing.T) {
	pol := acl.NewPolicy()
	u := acl.User("u")
	g := acl.Group("g")
	pol.AddMember(g, u)
	pol.Grant(g, "p", acl.Read)
	ev := NewEvaluator(pol)

	if !ev.Allowed(u, "p", acl.Read) {
		t.Fatal("组授权应被继承")
	}
	pol.Grant(u, "p", acl.Write) // 组无写权限，直接授权补齐
	if !ev.Allowed(u, "p", acl.Write) {
		t.Fatal("直接授权应优先于组判定并生效")
	}
	pol.Revoke(g, "p", acl.Read) // 撤销组授权后读权限消失
	if ev.Allowed(u, "p", acl.Read) {
		t.Fatal("组授权撤销后继承应失效")
	}
}

// TestGroupVisitsNotLinear 组展开访问数不随求值次数增长（100/10000 两档）。
func TestGroupVisitsNotLinear(t *testing.T) {
	for _, n := range []int{100, 10000} {
		t.Run(fmt.Sprintf("groups=%d", n), func(t *testing.T) {
			pol := acl.NewPolicy()
			u := acl.User("u")
			prev := u
			for i := 0; i < n; i++ {
				g := acl.Group(fmt.Sprintf("g%d", i))
				pol.AddMember(g, prev)
				prev = g
			}
			pol.Grant(prev, "deep", acl.Read) // 最深的组授权

			ev := NewEvaluator(pol)
			const rounds = 5
			for i := 0; i < rounds; i++ {
				if !ev.Allowed(u, "deep", acl.Read) {
					t.Fatalf("round %d: 深层嵌套组授权应可判定", i)
				}
			}
			if got := ev.GroupVisits(); got != n {
				t.Fatalf("GroupVisits=%d, want %d（每组至多访问一次，与求值次数无关）", got, n)
			}
		})
	}
}

// TestGroupCycle 组关系成环时展开终止且结果正确。
func TestGroupCycle(t *testing.T) {
	pol := acl.NewPolicy()
	u := acl.User("u")
	g1, g2 := acl.Group("g1"), acl.Group("g2")
	pol.AddMember(g1, u)
	pol.AddMember(g2, g1)
	pol.AddMember(g1, g2) // 环
	pol.Grant(g2, "p", acl.Read)

	ev := NewEvaluator(pol)
	if !ev.Allowed(u, "p", acl.Read) {
		t.Fatal("环内组授权应可判定")
	}
	if ev.GroupVisits() != 2 {
		t.Fatalf("GroupVisits=%d, want 2（环不重复访问）", ev.GroupVisits())
	}
}
