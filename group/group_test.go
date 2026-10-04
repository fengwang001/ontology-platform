package group

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestValidName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"", false}, {"*", false},
		{"g", true}, {"d-1", true},
		{strings.Repeat("x", 64), true},
		{strings.Repeat("x", 65), false},
	}
	for _, c := range cases {
		if got := ValidName(c.name); got != c.want {
			t.Errorf("ValidName(%q)=%v want %v", c.name, got, c.want)
		}
	}
}

func TestNewInvalid(t *testing.T) {
	for _, g := range []int{0, 17} {
		if _, err := New(g); !errors.Is(err, ErrInvalid) {
			t.Errorf("New(%d) err=%v want ErrInvalid", g, err)
		}
	}
}

func TestStarImmutable(t *testing.T) {
	s, _ := New(4)
	if !errors.Is(s.RemoveGroup(Star), ErrInvalid) {
		t.Error("RemoveGroup(*) must be ErrInvalid")
	}
	if !errors.Is(s.SetPriority(Star, 5), ErrInvalid) {
		t.Error("SetPriority(*) must be ErrInvalid")
	}
	if err := s.AddDevice("d"); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(s.AddMember(Star, "d"), ErrInvalid) {
		t.Error("AddMember(*,d) must be ErrInvalid")
	}
	if !errors.Is(s.RemoveMember(Star, "d"), ErrInvalid) {
		t.Error("RemoveMember(*,d) must be ErrInvalid")
	}
	if pr, ok := s.Priority(Star); !ok || pr != -1 {
		t.Errorf("star priority = %d,%v want -1,true", pr, ok)
	}
	if got := s.GroupsOf("d"); !reflect.DeepEqual(got, []string{Star}) {
		t.Errorf("new device groups=%v want [*]", got)
	}
}

func TestMembershipLifecycle(t *testing.T) {
	s, _ := New(2)
	if err := s.AddGroup("g1", 10); err != nil {
		t.Fatal(err)
	}
	if err := s.AddGroup("g2", 10); err != nil {
		t.Fatal(err)
	}
	if err := s.AddDevice("d"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMember("g1", "d"); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(s.AddMember("g1", "d"), ErrExists) {
		t.Error("duplicate AddMember must be ErrExists")
	}
	if err := s.AddMember("g2", "d"); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(s.AddMember("g2", "d"), ErrExists) {
		t.Fatal("dup must remain ErrExists")
	}
	// 不存在的第三组：先报 NotFound 而不是 TooManyGroups。
	if !errors.Is(s.AddMember("nope", "d"), ErrNotFound) {
		t.Error("missing group must be ErrNotFound")
	}
	if err := s.AddGroup("g3", 1); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(s.AddMember("g3", "d"), ErrTooManyGroups) {
		t.Errorf("over gmax must be ErrTooManyGroups")
	}
	if err := s.RemoveMember("g2", "d"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMember("g3", "d"); err != nil {
		t.Errorf("after freeing a slot AddMember should succeed: %v", err)
	}
	if !errors.Is(s.RemoveMember("g2", "d"), ErrNotFound) {
		t.Error("removing absent member must be ErrNotFound")
	}
	if !errors.Is(s.RemoveGroup("g1"), ErrNotEmpty) {
		t.Error("non-empty group removal must be ErrNotEmpty")
	}
}

func TestNotFoundOrdering(t *testing.T) {
	s, _ := New(4)
	if !errors.Is(s.RemoveDevice("d"), ErrNotFound) {
		t.Error("remove missing device ErrNotFound")
	}
	if !errors.Is(s.SetPriority("g", -2), ErrInvalid) {
		t.Error("bad pr is ErrInvalid before NotFound")
	}
	if !errors.Is(s.SetPriority("g", 1001), ErrInvalid) {
		t.Error("pr>1000 is ErrInvalid")
	}
	if !errors.Is(s.SetPriority("g", 5), ErrNotFound) {
		t.Error("SetPriority missing group ErrNotFound")
	}
	if !errors.Is(s.AddGroup("g", -1), ErrInvalid) {
		t.Error("negative pr ErrInvalid")
	}
	if !errors.Is(s.RemoveGroup("g"), ErrNotFound) {
		t.Error("RemoveGroup missing ErrNotFound")
	}
	if !errors.Is(s.AddMember("g", "d"), ErrNotFound) {
		t.Error("AddMember both missing ErrNotFound")
	}
	if err := s.AddDevice("d"); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(s.AddMember("g", "d"), ErrNotFound) {
		t.Error("missing group still ErrNotFound")
	}
}

func TestCloneIsolation(t *testing.T) {
	s, _ := New(4)
	if err := s.AddGroup("g", 5); err != nil {
		t.Fatal(err)
	}
	if err := s.AddDevice("d"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMember("g", "d"); err != nil {
		t.Fatal(err)
	}
	c := s.Clone()
	if err := c.RemoveDevice("d"); err != nil {
		t.Fatal(err)
	}
	if !s.HasDevice("d") || s.MemberCount("g") != 1 {
		t.Error("mutating clone must not affect original")
	}
	if c.MemberCount("g") != 0 {
		t.Error("clone device removal must clear memberships")
	}
}
