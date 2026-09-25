package access

import (
	"strconv"
	"testing"

	"ontology/acl"
)

func TestAllowedTable(t *testing.T) {
	// 遍历 主体 × 属性 × 动作 × 默认策略(默认拒绝) × 授权形态。
	attrs := []string{"a", "b", "c"}
	acts := []struct {
		name string
		perm acl.Perm
	}{{"read", acl.Read}, {"write", acl.Write}}

	cases := []struct {
		name  string
		setup func(s *acl.Store, u, g acl.Subject)
		want  map[string]map[acl.Perm]bool // attr -> perm -> allowed
	}{
		{
			name:  "default deny everywhere",
			setup: func(s *acl.Store, u, g acl.Subject) {},
			want: map[string]map[acl.Perm]bool{
				"a": {acl.Read: false, acl.Write: false},
				"b": {acl.Read: false, acl.Write: false},
			},
		},
		{
			name: "group union grant",
			setup: func(s *acl.Store, u, g acl.Subject) {
				s.Grant(g, "a", acl.Read)
			},
			want: map[string]map[acl.Perm]bool{
				"a": {acl.Read: true, acl.Write: false},
				"b": {acl.Read: false, acl.Write: false},
			},
		},
		{
			name: "direct deny overrides group allow",
			setup: func(s *acl.Store, u, g acl.Subject) {
				s.Grant(g, "a", acl.Read)
				s.Revoke(u, "a", acl.Read)
			},
			want: map[string]map[acl.Perm]bool{
				"a": {acl.Read: false, acl.Write: false},
			},
		},
		{
			name: "direct allow overrides group deny",
			setup: func(s *acl.Store, u, g acl.Subject) {
				s.Revoke(g, "b", acl.Write)
				s.Grant(u, "b", acl.Write)
			},
			want: map[string]map[acl.Perm]bool{
				"b": {acl.Write: true, acl.Read: false},
			},
		},
		{
			name: "nested group inheritance",
			setup: func(s *acl.Store, u, g acl.Subject) {
				s.AddMember("outer", g)
				s.Grant(acl.GroupSubject("outer"), "c", acl.Read)
			},
			want: map[string]map[acl.Perm]bool{
				"c": {acl.Read: true, acl.Write: false},
			},
		},
		{
			name: "diamond groups visited once via union",
			setup: func(s *acl.Store, u, g acl.Subject) {
				s.AddMember("l1", u)
				s.AddMember("l2a", acl.GroupSubject("l1"))
				s.AddMember("l2b", acl.GroupSubject("l1"))
				s.AddMember("top", acl.GroupSubject("l2a"))
				s.AddMember("top", acl.GroupSubject("l2b"))
				s.Grant(acl.GroupSubject("top"), "a", acl.Write)
			},
			want: map[string]map[acl.Perm]bool{
				"a": {acl.Write: true, acl.Read: false},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := acl.NewStore()
			u, g := acl.User("u"), acl.GroupSubject("g")
			s.AddMember("g", u)
			tc.setup(s, u, g)
			c := NewChecker(s, u)
			for _, attr := range attrs {
				for _, act := range acts {
					want, present := tc.want[attr][act.perm]
					if !present {
						want = false
					}
					if got := c.Allowed(attr, act.perm); got != want {
						t.Errorf("Allowed(%s,%s) = %v, want %v", attr, act.name, got, want)
					}
				}
			}
		})
	}
}

func TestClosureExpandedOnce(t *testing.T) {
	// 100 / 10000 两档：判定 N 个属性时，组展开只发生一次，访问数与属性数无关。
	for _, nGroups := range []int{100, 10000} {
		s := acl.NewStore()
		u := acl.User("u")
		s.AddMember("g0", u)
		for i := 1; i < nGroups; i++ {
			s.AddMember("g"+strconv.Itoa(i), acl.GroupSubject("g"+strconv.Itoa(i-1)))
		}
		c := NewChecker(s, u)
		c.Allowed("x", acl.Read)
		first := c.Expansions()
		for i := 0; i < 50; i++ {
			c.Allowed("attr"+strconv.Itoa(i), acl.Read)
		}
		if c.Expansions() != first {
			t.Fatalf("n=%d expansions grew %d -> %d", nGroups, first, c.Expansions())
		}
		if first != nGroups+1 {
			t.Fatalf("n=%d expansions = %d, want %d (subject + chain, each once)",
				nGroups, first, nGroups+1)
		}
	}
}

func TestExpansionRefreshedOnMutation(t *testing.T) {
	s := acl.NewStore()
	u := acl.User("u")
	c := NewChecker(s, u)
	c.Allowed("a", acl.Read)
	s.AddMember("g", u)
	s.Grant(acl.GroupSubject("g"), "a", acl.Read)
	if !c.Allowed("a", acl.Read) {
		t.Fatal("grant after expansion must take effect")
	}
}
