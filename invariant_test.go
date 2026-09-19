package ontology

import "testing"

func TestCheckInvariantClean(t *testing.T) {
	s := newStore(t)
	declare(t, s, ltOwns())
	declare(t, s, ltMember())
	addObjects(t, s, "User", "u1", "u2")
	addObjects(t, s, "Team", "t1")
	addObjects(t, s, "Doc", "d1")
	link(t, s, "owns", key("User", "u1"), key("Doc", "d1"))
	link(t, s, "member", key("Team", "t1"), key("User", "u1"))
	link(t, s, "member", key("Team", "t1"), key("User", "u2"))
	requireInvariantOK(t, s)
}

func TestCheckInvariantDetectsForwardOnly(t *testing.T) {
	s := newStore(t)
	declare(t, s, ltOwns())
	addObjects(t, s, "User", "u1")
	addObjects(t, s, "Doc", "d1")
	link(t, s, "owns", key("User", "u1"), key("Doc", "d1"))

	// 人为破坏：只删反向索引，制造孤链。
	s.mu.Lock()
	delete(s.bwd["owns"][key("Doc", "d1")], key("User", "u1"))
	s.mu.Unlock()

	vs := s.CheckInvariant()
	if len(vs) != 1 {
		t.Fatalf("want 1 violation, got %v", vs)
	}
	v := vs[0]
	if v.LinkType != "owns" || v.Side != "forward" ||
		v.Source != key("User", "u1") || v.Target != key("Doc", "d1") {
		t.Fatalf("violation missing context: %+v", v)
	}
}

func TestCheckInvariantDetectsReverseOnly(t *testing.T) {
	s := newStore(t)
	declare(t, s, ltOwns())
	addObjects(t, s, "User", "u1")
	addObjects(t, s, "Doc", "d1")
	link(t, s, "owns", key("User", "u1"), key("Doc", "d1"))

	s.mu.Lock()
	delete(s.fwd["owns"][key("User", "u1")], key("Doc", "d1"))
	s.mu.Unlock()

	vs := s.CheckInvariant()
	if len(vs) != 1 || vs[0].Side != "reverse" || vs[0].LinkType != "owns" {
		t.Fatalf("want reverse-side violation, got %v", vs)
	}
}

func TestCheckInvariantDetectsDangling(t *testing.T) {
	s := newStore(t)
	declare(t, s, ltOwns())
	addObjects(t, s, "User", "u1")
	addObjects(t, s, "Doc", "d1")
	link(t, s, "owns", key("User", "u1"), key("Doc", "d1"))

	s.mu.Lock()
	delete(s.objects, key("Doc", "d1"))
	s.mu.Unlock()

	vs := s.CheckInvariant()
	if len(vs) != 1 || vs[0].Side != "dangling" || vs[0].Target != key("Doc", "d1") {
		t.Fatalf("want dangling violation, got %v", vs)
	}
}
