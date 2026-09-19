package ontology

import (
	"errors"
	"testing"
)

func TestCascadeDeleteRecursive(t *testing.T) {
	s := newStore(t)
	declare(t, s, ltDepends())
	addObjects(t, s, "Node", "n1", "n2", "n3", "n4")
	link(t, s, "depends", key("Node", "n1"), key("Node", "n2"))
	link(t, s, "depends", key("Node", "n2"), key("Node", "n3"))
	link(t, s, "depends", key("Node", "n3"), key("Node", "n4"))
	must(t, s.DeleteObject(key("Node", "n1")))
	for _, id := range []string{"n1", "n2", "n3", "n4"} {
		if s.HasObject(key("Node", id)) {
			t.Fatalf("%s should be cascade-deleted", id)
		}
	}
	if got := s.Links("depends"); len(got) != 0 {
		t.Fatalf("all links must be gone, got %v", got)
	}
	requireInvariantOK(t, s)
}

func TestSetNullKeepsPeer(t *testing.T) {
	s := newStore(t)
	declare(t, s, ltMember())
	addObjects(t, s, "Team", "t1")
	addObjects(t, s, "User", "u1", "u2")
	link(t, s, "member", key("Team", "t1"), key("User", "u1"))
	link(t, s, "member", key("Team", "t1"), key("User", "u2"))
	must(t, s.DeleteObject(key("Team", "t1")))
	if !s.HasObject(key("User", "u1")) || !s.HasObject(key("User", "u2")) {
		t.Fatal("SET_NULL must keep peer objects")
	}
	if got := s.Links("member"); len(got) != 0 {
		t.Fatalf("links must be removed, got %v", got)
	}
	requireInvariantOK(t, s)
}

func TestRestrictRollsBackEverything(t *testing.T) {
	s := newStore(t)
	declare(t, s, ltOwns())
	declare(t, s, ltGuards())
	addObjects(t, s, "User", "u1")
	addObjects(t, s, "Team", "t1")
	addObjects(t, s, "Doc", "d1", "d2")
	link(t, s, "owns", key("User", "u1"), key("Doc", "d1"))
	link(t, s, "owns", key("User", "u1"), key("Doc", "d2"))
	link(t, s, "guards", key("Team", "t1"), key("Doc", "d2"))
	err := s.DeleteObject(key("User", "u1"))
	var re *RestrictError
	if !errors.As(err, &re) {
		t.Fatalf("want RestrictError, got %v", err)
	}
	// 路径：u1 --owns--> d2，随后 d2 被 guards(RESTRICT) 链 t1->d2 拦住。
	if len(re.Path) != 2 {
		t.Fatalf("want path length 2, got %v", re.Path)
	}
	if re.Path[0].LinkType != "owns" || re.Path[0].Source != key("User", "u1") {
		t.Fatalf("bad path head: %+v", re.Path[0])
	}
	if re.Path[1].LinkType != "guards" || re.Path[1].Target != key("Doc", "d2") {
		t.Fatalf("bad path tail: %+v", re.Path[1])
	}
	// 完整回滚：对象与链全部保持删除前状态。
	for _, k := range []ObjectKey{key("User", "u1"), key("Doc", "d1"), key("Doc", "d2"), key("Team", "t1")} {
		if !s.HasObject(k) {
			t.Fatalf("%v must survive rolled-back delete", k)
		}
	}
	requireKeys(t, s.LinksFrom("owns", key("User", "u1")), key("Doc", "d1"), key("Doc", "d2"))
	requireKeys(t, s.LinksFrom("guards", key("Team", "t1")), key("Doc", "d2"))
	requireInvariantOK(t, s)
}

func TestCascadeRingTerminatesAndIdempotent(t *testing.T) {
	s := newStore(t)
	declare(t, s, ltDepends())
	addObjects(t, s, "Node", "n1", "n2", "n3")
	link(t, s, "depends", key("Node", "n1"), key("Node", "n2"))
	link(t, s, "depends", key("Node", "n2"), key("Node", "n3"))
	link(t, s, "depends", key("Node", "n3"), key("Node", "n1"))
	must(t, s.DeleteObject(key("Node", "n1")))
	for _, id := range []string{"n1", "n2", "n3"} {
		if s.HasObject(key("Node", id)) {
			t.Fatalf("%s should be deleted exactly once", id)
		}
	}
	requireInvariantOK(t, s)
	var nf *ObjectNotFoundError
	if err := s.DeleteObject(key("Node", "n1")); !errors.As(err, &nf) {
		t.Fatalf("want ObjectNotFoundError, got %v", err)
	}
}

func TestRingWithRestrictRollsBack(t *testing.T) {
	s := newStore(t)
	declare(t, s, ltDepends())
	declare(t, s, LinkType{Name: "protects", SourceType: "Node", TargetType: "Doc",
		Cardinality: ManyToMany, Cascade: CascadeRestrict})
	addObjects(t, s, "Node", "n1", "n2")
	addObjects(t, s, "Doc", "d1")
	link(t, s, "depends", key("Node", "n1"), key("Node", "n2"))
	link(t, s, "depends", key("Node", "n2"), key("Node", "n1"))
	link(t, s, "protects", key("Node", "n2"), key("Doc", "d1"))
	var re *RestrictError
	if err := s.DeleteObject(key("Node", "n1")); !errors.As(err, &re) {
		t.Fatalf("want RestrictError, got %v", err)
	}
	if !s.HasObject(key("Node", "n1")) || !s.HasObject(key("Node", "n2")) {
		t.Fatal("ring delete with RESTRICT must roll back")
	}
	requireKeys(t, s.LinksFrom("depends", key("Node", "n1")), key("Node", "n2"))
	requireKeys(t, s.LinksFrom("depends", key("Node", "n2")), key("Node", "n1"))
	requireInvariantOK(t, s)
}

func TestSelfLoopDelete(t *testing.T) {
	s := newStore(t)
	declare(t, s, ltDepends())
	addObjects(t, s, "Node", "n1")
	link(t, s, "depends", key("Node", "n1"), key("Node", "n1"))
	must(t, s.DeleteObject(key("Node", "n1")))
	if s.HasObject(key("Node", "n1")) {
		t.Fatal("self-loop node must be deleted")
	}
	requireInvariantOK(t, s)
}
