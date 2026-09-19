package ontology

import (
	"errors"
	"testing"
)

// 必选性违约只在提交时校验，且一次报出全部缺失。
func TestCommitReportsAllRequiredViolations(t *testing.T) {
	s := newStore(t)
	lt := ltMember()
	lt.SourceRequired = true // 每个 Team 必须有成员
	lt.TargetRequired = true // 每个 User 必须属于某个 Team
	declare(t, s, lt)
	addObjects(t, s, "Team", "t1", "t2")
	addObjects(t, s, "User", "u1", "u2")
	// 只建 t1->u1，t2 缺源侧必选、u2 缺目标侧必选。
	err := s.Begin().Link("member", key("Team", "t1"), key("User", "u1")).Commit()
	var reqErr *RequiredError
	if !errors.As(err, &reqErr) {
		t.Fatalf("want RequiredError, got %v", err)
	}
	if len(reqErr.Violations) != 2 {
		t.Fatalf("want 2 violations reported at once, got %v", reqErr.Violations)
	}
	want := []RequiredViolation{
		{LinkType: "member", Side: "source", Object: key("Team", "t2")},
		{LinkType: "member", Side: "target", Object: key("User", "u2")},
	}
	for i, w := range want {
		if reqErr.Violations[i] != w {
			t.Fatalf("violation %d: got %+v want %+v", i, reqErr.Violations[i], w)
		}
	}
	// 提交失败必须整体回滚：t1->u1 不应残留。
	if got := s.LinksFrom("member", key("Team", "t1")); len(got) != 0 {
		t.Fatalf("failed commit must roll back, got links %v", got)
	}
	requireInvariantOK(t, s)
	// 补齐后提交成功。
	must(t, s.Begin().
		Link("member", key("Team", "t1"), key("User", "u1")).
		Link("member", key("Team", "t2"), key("User", "u2")).
		Commit())
	requireInvariantOK(t, s)
}

// 非事务的 Link 不做必选性校验，只在 Commit 时校验。
func TestRequiredOnlyCheckedAtCommit(t *testing.T) {
	s := newStore(t)
	lt := ltMember()
	lt.SourceRequired = true
	declare(t, s, lt)
	addObjects(t, s, "Team", "t1")
	addObjects(t, s, "User", "u1")
	link(t, s, "member", key("Team", "t1"), key("User", "u1"))
	requireInvariantOK(t, s)
}

func TestCommitBatchErrorPropagates(t *testing.T) {
	s := newStore(t)
	declare(t, s, ltSpouse())
	addObjects(t, s, "User", "u1", "u2", "u3")
	err := s.Begin().
		Link("spouse", key("User", "u1"), key("User", "u2")).
		Link("spouse", key("User", "u1"), key("User", "u3")).
		Commit()
	var batchErr *BatchError
	var cardErr *CardinalityError
	if !errors.As(err, &batchErr) || batchErr.Index != 1 {
		t.Fatalf("want BatchError at index 1, got %v", err)
	}
	if !errors.As(err, &cardErr) || cardErr.Kind != ViolationOneToOneSource {
		t.Fatalf("want wrapped CardinalityError, got %v", err)
	}
	if got := s.LinksFrom("spouse", key("User", "u1")); len(got) != 0 {
		t.Fatalf("failed commit must roll back, got %v", got)
	}
}
