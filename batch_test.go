package ontology

import (
	"errors"
	"testing"
)

func TestBatchAtomicRollback(t *testing.T) {
	s := newStore(t)
	declare(t, s, ltSpouse())
	addObjects(t, s, "User", "u1", "u2", "u3", "u4")
	err := s.ApplyBatch([]BatchOp{
		LinkOp("spouse", key("User", "u1"), key("User", "u2")),
		LinkOp("spouse", key("User", "u3"), key("User", "u4")),
		LinkOp("spouse", key("User", "u1"), key("User", "u4")), // 触发基数违约
	})
	var be *BatchError
	if !errors.As(err, &be) || be.Index != 2 || be.Op.LinkType != "spouse" {
		t.Fatalf("want BatchError at index 2, got %v", err)
	}
	var ce *CardinalityError
	if !errors.As(err, &ce) {
		t.Fatalf("want wrapped CardinalityError, got %v", err)
	}
	if got := s.Links("spouse"); len(got) != 0 {
		t.Fatalf("failed batch must fully roll back, got %v", got)
	}
	requireInvariantOK(t, s)
}

func TestBatchDuplicateSameLink(t *testing.T) {
	s := newStore(t)
	declare(t, s, ltOwns())
	addObjects(t, s, "User", "u1")
	addObjects(t, s, "Doc", "d1")
	dup := LinkOp("owns", key("User", "u1"), key("Doc", "d1"))
	err := s.ApplyBatch([]BatchOp{dup, dup})
	var be *BatchError
	var de *DuplicateLinkError
	if !errors.As(err, &be) || be.Index != 1 || !errors.As(err, &de) {
		t.Fatalf("want duplicate at index 1, got %v", err)
	}
	if got := s.Links("owns"); len(got) != 0 {
		t.Fatalf("batch must roll back, got %v", got)
	}
}

func TestBatchLinkThenUnlinkSameLink(t *testing.T) {
	s := newStore(t)
	declare(t, s, ltOwns())
	addObjects(t, s, "User", "u1")
	addObjects(t, s, "Doc", "d1")
	must(t, s.ApplyBatch([]BatchOp{
		LinkOp("owns", key("User", "u1"), key("Doc", "d1")),
		UnlinkOp("owns", key("User", "u1"), key("Doc", "d1")),
	}))
	if got := s.Links("owns"); len(got) != 0 {
		t.Fatalf("link-then-unlink must net to nothing, got %v", got)
	}
	if !s.HasObject(key("User", "u1")) || !s.HasObject(key("Doc", "d1")) {
		t.Fatal("objects must survive")
	}
	requireInvariantOK(t, s)
}

func TestBatchLinkThenCascadeDelete(t *testing.T) {
	s := newStore(t)
	declare(t, s, ltOwns()) // CASCADE
	addObjects(t, s, "User", "u1")
	addObjects(t, s, "Doc", "d1")
	// 批内建立的链被同批的级联删除波及：删除方生效，链与对端一并结算。
	must(t, s.ApplyBatch([]BatchOp{
		LinkOp("owns", key("User", "u1"), key("Doc", "d1")),
		DeleteOp(key("User", "u1")),
	}))
	if s.HasObject(key("User", "u1")) || s.HasObject(key("Doc", "d1")) {
		t.Fatal("cascade delete must settle objects created/linked in same batch")
	}
	if got := s.Links("owns"); len(got) != 0 {
		t.Fatalf("no link may survive, got %v", got)
	}
	requireInvariantOK(t, s)
}

func TestBatchMixedSuccess(t *testing.T) {
	s := newStore(t)
	declare(t, s, ltOwns())
	declare(t, s, ltMember())
	addObjects(t, s, "User", "u1", "u2")
	addObjects(t, s, "Team", "t1")
	addObjects(t, s, "Doc", "d1", "d2")
	link(t, s, "owns", key("User", "u1"), key("Doc", "d1"))
	must(t, s.ApplyBatch([]BatchOp{
		UnlinkOp("owns", key("User", "u1"), key("Doc", "d1")),
		LinkOp("owns", key("User", "u2"), key("Doc", "d2")),
		LinkOp("member", key("Team", "t1"), key("User", "u1")),
		DeleteOp(key("Doc", "d1")),
	}))
	requireKeys(t, s.LinksFrom("owns", key("User", "u2")), key("Doc", "d2"))
	requireKeys(t, s.LinksFrom("member", key("Team", "t1")), key("User", "u1"))
	if s.HasObject(key("Doc", "d1")) {
		t.Fatal("d1 must be deleted")
	}
	requireInvariantOK(t, s)
}

func TestBatchRestrictRollbackInsideBatch(t *testing.T) {
	s := newStore(t)
	declare(t, s, ltOwns())
	declare(t, s, ltGuards())
	addObjects(t, s, "User", "u1")
	addObjects(t, s, "Team", "t1")
	addObjects(t, s, "Doc", "d1")
	link(t, s, "owns", key("User", "u1"), key("Doc", "d1"))
	link(t, s, "guards", key("Team", "t1"), key("Doc", "d1"))
	err := s.ApplyBatch([]BatchOp{
		LinkOp("member2", key("Team", "t1"), key("User", "u1")), // 未声明，必然失败
	})
	var be *BatchError
	if !errors.As(err, &be) || be.Index != 0 {
		t.Fatalf("want BatchError at 0, got %v", err)
	}
	err = s.ApplyBatch([]BatchOp{DeleteOp(key("User", "u1"))})
	var re *RestrictError
	if !errors.As(err, &re) {
		t.Fatalf("want RestrictError, got %v", err)
	}
	if !s.HasObject(key("User", "u1")) || !s.HasObject(key("Doc", "d1")) {
		t.Fatal("restrict inside batch must roll back the whole batch")
	}
	requireInvariantOK(t, s)
}
