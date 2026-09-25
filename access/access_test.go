package access

import (
	"fmt"
	"testing"

	"ontology/acl"
)

// TestEvaluateMatrix 表驱动遍历 主体×属性×动作 的判定矩阵，
// 覆盖默认拒绝、组继承（嵌套）、直接授权覆盖组授权、撤销。
func TestEvaluateMatrix(t *testing.T) {
	store := acl.NewStore()
	g1 := acl.Group("g1")
	g2 := acl.Group("g2")
	alice := acl.User("alice")
	bob := acl.User("bob")
	store.AddMember(g1, g2)
	store.AddMember(g2, alice)
	store.Grant(g1, "a", acl.Read)
	store.Grant(g1, "b", acl.Write)
	store.Grant(alice, "c", acl.Read)
	store.Deny(alice, "a", acl.Read)
	ev := NewEvaluator(store)
	cases := []struct {
		name    string
		subject acl.Subject
		prop    string
		action  acl.Action
		want    bool
	}{
		{"直接拒绝覆盖嵌套组继承", alice, "a", acl.Read, false},
		{"嵌套组继承写", alice, "b", acl.Write, true},
		{"直接授权", alice, "c", acl.Read, true},
		{"直接授权不跨动作", alice, "c", acl.Write, false},
		{"默认拒绝-未知属性", alice, "zzz", acl.Read, false},
		{"默认拒绝-无关系主体", bob, "a", acl.Read, false},
		{"默认拒绝-无关系主体写", bob, "b", acl.Write, false},
		{"组主体自身判定", g1, "a", acl.Read, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ev.Allowed(tc.subject, tc.prop, tc.action); got != tc.want {
				t.Fatalf("Allowed(%v,%s,%v)=%v, want %v", tc.subject, tc.prop, tc.action, got, tc.want)
			}
		})
	}
	store.Revoke(g1, "b", acl.Write)
	if ev.Allowed(alice, "b", acl.Write) {
		t.Fatal("撤销组授权后仍允许写")
	}
	store.Revoke(alice, "a", acl.Read)
	if !ev.Allowed(alice, "a", acl.Read) {
		t.Fatal("撤销直接拒绝后组继承应恢复")
	}
}

// TestGroupExpansionNotLinear 用非导出计数器 expansions 证明：
// 组展开的访问数不随组总数线性增长（100/10000 两档），且重复判定命中缓存。
func TestGroupExpansionNotLinear(t *testing.T) {
	for _, n := range []int{100, 10000} {
		t.Run(fmt.Sprintf("groups=%d", n), func(t *testing.T) {
			store := acl.NewStore()
			u := acl.User("u")
			prev := u
			for i := 0; i < n; i++ {
				g := acl.Group(fmt.Sprintf("g%d", i))
				store.AddMember(g, prev)
				prev = g
			}
			ev := NewEvaluator(store)
			ev.Allowed(u, "p", acl.Read)
			first := ev.expansions
			if first != 1 {
				t.Fatalf("n=%d: 首次判定展开次数=%d, want 1（记忆化单次展开）", n, first)
			}
			for i := 0; i < 50; i++ {
				ev.Allowed(u, "p", acl.Read)
			}
			if ev.expansions != first {
				t.Fatalf("n=%d: 重复判定后展开次数=%d, want 不变 %d", n, ev.expansions, first)
			}
		})
	}
}

// TestUnionSemantics 主体自身与所在任一组的授权并集生效。
func TestUnionSemantics(t *testing.T) {
	store := acl.NewStore()
	ga, gb := acl.Group("ga"), acl.Group("gb")
	u := acl.User("u")
	store.AddMember(ga, u)
	store.AddMember(gb, u)
	store.Grant(ga, "x", acl.Read)
	store.Grant(gb, "y", acl.Read)
	store.Grant(u, "z", acl.Read)
	ev := NewEvaluator(store)
	for _, prop := range []string{"x", "y", "z"} {
		if !ev.Allowed(u, prop, acl.Read) {
			t.Fatalf("并集失效：%s 应可读", prop)
		}
	}
	if ev.Allowed(u, "w", acl.Read) {
		t.Fatal("默认拒绝失效：w 不应可读")
	}
}
