package acl

import "testing"

func TestGrantRevokeDirectEntry(t *testing.T) {
	u := User("u")
	cases := []struct {
		name  string
		apply func(s *Store)
		allow bool
		set   bool
	}{
		{"no entry default unset", func(s *Store) {}, false, false},
		{"grant read", func(s *Store) { s.Grant(u, "a", Read) }, true, true},
		{"revoke is explicit deny", func(s *Store) { s.Revoke(u, "a", Read) }, false, true},
		{"write independent of read", func(s *Store) { s.Grant(u, "a", Read) }, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStore()
			tc.apply(s)
			allow, set := s.DirectEntry(u, "a", Read)
			if allow != tc.allow || set != tc.set {
				t.Fatalf("DirectEntry = (%v,%v), want (%v,%v)", allow, set, tc.allow, tc.set)
			}
		})
	}
}

func TestMembershipReverseIndex(t *testing.T) {
	s := NewStore()
	u, g1, g2 := User("u"), GroupSubject("g1"), GroupSubject("g2")
	s.AddMember("g1", u)
	s.AddMember("g2", g1)
	if got := s.ParentGroups(u); len(got) != 1 || got[0] != "g1" {
		t.Fatalf("parents of u = %v", got)
	}
	if got := s.ParentGroups(g1); len(got) != 1 || got[0] != "g2" {
		t.Fatalf("parents of g1 = %v", got)
	}
	s.RemoveMember("g1", u)
	if got := s.ParentGroups(u); len(got) != 0 {
		t.Fatalf("after remove parents = %v, want empty", got)
	}
	// mutations 计数：两次 add + 一次 remove + grant。
	s.Grant(u, "a", Read)
	if s.Mutations() != 4 {
		t.Fatalf("mutations = %d, want 4", s.Mutations())
	}
}
