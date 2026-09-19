package ontology

import (
	"errors"
	"testing"
)

func TestLinkAndTraverseMirror(t *testing.T) {
	s := newStore(t)
	declare(t, s, ltOwns())
	addObjects(t, s, "User", "u1", "u2")
	addObjects(t, s, "Doc", "d1", "d2")

	link(t, s, "owns", key("User", "u1"), key("Doc", "d1"))
	link(t, s, "owns", key("User", "u1"), key("Doc", "d2"))
	link(t, s, "owns", key("User", "u2"), key("Doc", "d1"))

	requireKeys(t, s.LinksFrom("owns", key("User", "u1")), key("Doc", "d1"), key("Doc", "d2"))
	requireKeys(t, s.LinksTo("owns", key("Doc", "d1")), key("User", "u1"), key("User", "u2"))
	requireKeys(t, s.LinksTo("owns", key("Doc", "d2")), key("User", "u1"))
	requireInvariantOK(t, s)

	must(t, s.Unlink("owns", key("User", "u1"), key("Doc", "d1")))
	requireKeys(t, s.LinksFrom("owns", key("User", "u1")), key("Doc", "d2"))
	requireKeys(t, s.LinksTo("owns", key("Doc", "d1")), key("User", "u2"))
	requireInvariantOK(t, s)
}

func TestUnlinkMissing(t *testing.T) {
	s := newStore(t)
	declare(t, s, ltOwns())
	addObjects(t, s, "User", "u1")
	addObjects(t, s, "Doc", "d1")
	err := s.Unlink("owns", key("User", "u1"), key("Doc", "d1"))
	var nf *LinkNotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("want LinkNotFoundError, got %v", err)
	}
}

func TestFiveErrorCategoriesDistinguishable(t *testing.T) {
	s := newStore(t)
	declare(t, s, ltSpouse())
	declare(t, s, ltOwns())
	addObjects(t, s, "User", "u1", "u2", "u3")
	addObjects(t, s, "Doc", "d1")

	// 1. 端点类型不符
	err := s.Link("owns", key("Doc", "d1"), key("Doc", "d1"))
	var typeErr *EndpointTypeError
	if !errors.As(err, &typeErr) || typeErr.LinkType != "owns" {
		t.Fatalf("want EndpointTypeError, got %v", err)
	}

	// 2. 端点对象不存在
	err = s.Link("owns", key("User", "ghost"), key("Doc", "d1"))
	var nfErr *ObjectNotFoundError
	if !errors.As(err, &nfErr) || nfErr.Side != "source" || nfErr.Source.ID != "ghost" {
		t.Fatalf("want ObjectNotFoundError(source), got %v", err)
	}
	err = s.Link("owns", key("User", "u1"), key("Doc", "ghost"))
	if !errors.As(err, &nfErr) || nfErr.Side != "target" {
		t.Fatalf("want ObjectNotFoundError(target), got %v", err)
	}

	// 3. 重复建链
	link(t, s, "owns", key("User", "u1"), key("Doc", "d1"))
	err = s.Link("owns", key("User", "u1"), key("Doc", "d1"))
	var dupErr *DuplicateLinkError
	if !errors.As(err, &dupErr) || dupErr.Source != key("User", "u1") || dupErr.Target != key("Doc", "d1") {
		t.Fatalf("want DuplicateLinkError, got %v", err)
	}

	// 4. ONE_TO_ONE：源已有链 / 目标被占用
	link(t, s, "spouse", key("User", "u1"), key("User", "u2"))
	err = s.Link("spouse", key("User", "u1"), key("User", "u3"))
	var cardErr *CardinalityError
	if !errors.As(err, &cardErr) || cardErr.Kind != ViolationOneToOneSource {
		t.Fatalf("want CardinalityError(ONE_TO_ONE_SOURCE), got %v", err)
	}
	err = s.Link("spouse", key("User", "u3"), key("User", "u2"))
	if !errors.As(err, &cardErr) || cardErr.Kind != ViolationOneToOneTarget {
		t.Fatalf("want CardinalityError(ONE_TO_ONE_TARGET), got %v", err)
	}

	// 5. ONE_TO_MANY：目标已归属其他源
	declare(t, s, ltMember())
	addObjects(t, s, "Team", "t1", "t2")
	link(t, s, "member", key("Team", "t1"), key("User", "u3"))
	err = s.Link("member", key("Team", "t2"), key("User", "u3"))
	if !errors.As(err, &cardErr) || cardErr.Kind != ViolationOneToManyTarget {
		t.Fatalf("want CardinalityError(ONE_TO_MANY_TARGET), got %v", err)
	}
	if cardErr.LinkType != "member" || cardErr.Source != key("Team", "t2") || cardErr.Target != key("User", "u3") {
		t.Fatalf("error missing context: %+v", cardErr)
	}
	requireInvariantOK(t, s)
}

func TestSelfReferenceLink(t *testing.T) {
	s := newStore(t)
	declare(t, s, ltDepends())
	addObjects(t, s, "Node", "n1", "n2")
	link(t, s, "depends", key("Node", "n1"), key("Node", "n1"))
	link(t, s, "depends", key("Node", "n1"), key("Node", "n2"))
	requireKeys(t, s.LinksFrom("depends", key("Node", "n1")), key("Node", "n1"), key("Node", "n2"))
	requireKeys(t, s.LinksTo("depends", key("Node", "n1")), key("Node", "n1"))
	requireInvariantOK(t, s)
}
