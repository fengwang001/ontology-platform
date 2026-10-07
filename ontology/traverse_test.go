package ontology

import (
	"errors"
	"fmt"
	"testing"
)

const lt LinkTypeID = "rel"

func buildGraph(t *testing.T) (*Store, *ACL, Actor) {
	t.Helper()
	s := NewStore()
	s.PutLinkType(LinkType{ID: lt, Cost: 1})
	acl := NewACL(s)
	actor := Actor{ID: "alice"}
	return s, acl, actor
}

func addObject(s *Store, acl *ACL, actor string, id string) {
	s.PutObject(Object{ID: id, Type: "T"})
	s.GrantObjectSee(id, actor)
}

func addLink(s *Store, acl *ACL, actor, from, to string) {
	if err := s.AddLink(Link{Type: lt, Source: from, Target: to}); err != nil {
		panic(err)
	}
	s.GrantLinkTraverse(Link{Type: lt, Source: from, Target: to}, actor)
}

// collectAll 用同一续读标记可重复使用的语义翻完所有页。
func collectAll(t *testing.T, it *Iterator, start string, depth, fanout int, actor Actor) ([]Object, Truncation) {
	t.Helper()
	var got []Object

	tok := ""
	var trunc Truncation
	for {
		p, err := it.Traverse(start, depth, fanout, tok, actor)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		got = append(got, p.Objects...)
		trunc = p.Truncation
		if p.Done {
			return got, trunc
		}
		tok = p.NextToken
	}
}

func ids(objs []Object) []string {
	out := make([]string, len(objs))
	for i, o := range objs {
		out[i] = o.ID
	}
	return out
}

// TestTruncationPriority：扇出截断与深度截断同时成立时，只报扇出。
func TestTruncationPriority(t *testing.T) {
	s, acl, actor := buildGraph(t)
	// A(depth0) -> B1,B2,B3（fanout=2 => 扇出截断）；B1 -> C1（depth=1 处本应展开 => 深度截断也成立）
	for _, id := range []string{"A", "B1", "B2", "B3", "C1"} {
		addObject(s, acl, actor.ID, id)
	}
	addLink(s, acl, actor.ID, "A", "B1")
	addLink(s, acl, actor.ID, "A", "B2")
	addLink(s, acl, actor.ID, "A", "B3")
	addLink(s, acl, actor.ID, "B1", "C1")

	it := NewIterator(s, acl)
	got, trunc := collectAll(t, it, "A", 1, 2, actor)
	if trunc != TruncFanout {
		t.Fatalf("want fanout priority, got %v, order=%v", trunc, ids(got))
	}
	want := []string{"A", "B1", "B2"}
	if fmt.Sprint(ids(got)) != fmt.Sprint(want) {
		t.Fatalf("order = %v, want %v", ids(got), want)
	}
}

// TestDepthTruncationAlone：只有深度截断。
func TestDepthTruncationAlone(t *testing.T) {
	s, acl, actor := buildGraph(t)
	for _, id := range []string{"A", "B", "C"} {
		addObject(s, acl, actor.ID, id)
	}
	addLink(s, acl, actor.ID, "A", "B")
	addLink(s, acl, actor.ID, "B", "C")

	it := NewIterator(s, acl)
	got, trunc := collectAll(t, it, "A", 1, 10, actor)
	if trunc != TruncDepth {
		t.Fatalf("want depth, got %v", trunc)
	}
	if fmt.Sprint(ids(got)) != "[A B]" {
		t.Fatalf("order = %v", ids(got))
	}
}

// TestSnapshotStabilityAcrossMutations：同一续读标记跨并发变更后的快照稳定性。
func TestSnapshotStabilityAcrossMutations(t *testing.T) {
	s, acl, actor := buildGraph(t)
	for _, id := range []string{"A", "B", "C"} {
		addObject(s, acl, actor.ID, id)
	}
	addLink(s, acl, actor.ID, "A", "B")
	addLink(s, acl, actor.ID, "A", "C")

	it := NewIterator(s, acl)
	it.SetPageSize(1)

	p1, err := it.Traverse("A", 2, 10, "", actor)
	if err != nil || len(p1.Objects) != 1 || p1.Objects[0].ID != "A" {
		t.Fatalf("page1 = %+v, err=%v", p1, err)
	}
	tok := p1.NextToken

	// 标记产生之后的变更：新增 D，删除 C，新增 B->D —— 对锚定快照均不可见。
	addObject(s, acl, actor.ID, "D")
	addLink(s, acl, actor.ID, "B", "D")
	s.RemoveLink(Link{Type: lt, Source: "A", Target: "C"})

	// 同一标记重复使用两次：必须返回完全相同的下一页。
	p2a, err := it.Traverse("A", 2, 10, tok, actor)
	if err != nil {
		t.Fatal(err)
	}
	p2b, err := it.Traverse("A", 2, 10, tok, actor)
	if err != nil {
		t.Fatal(err)
	}
	if p2a.Objects[0].ID != "B" || p2b.Objects[0].ID != "B" {
		t.Fatalf("repeated token drifted: %v vs %v", ids(p2a.Objects), ids(p2b.Objects))
	}

	// 用原标记链完整翻完，结果仍是旧快照下的 A,B,C（不含 D，不缺 C）。
	var got []Object
	cur := tok
	for {
		p, err := it.Traverse("A", 2, 10, cur, actor)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, p.Objects...)
		if p.Done {
			break
		}
		cur = p.NextToken
	}
	if fmt.Sprint(ids(got)) != "[B C]" {
		t.Fatalf("snapshot continuation = %v, want [B C]", ids(got))
	}
}

// TestInvisibleObjectsDoNotConsumeFanout：权限不可见对象不占扇出名额。
func TestInvisibleObjectsDoNotConsumeFanout(t *testing.T) {
	s, acl, actor := buildGraph(t)
	// 按标识排序：H1,H2 存在但 alice 无存在性权限；V1,V2 可见。fanout=2。
	for _, id := range []string{"A", "H1", "H2", "V1", "V2"} {
		s.PutObject(Object{ID: id, Type: "T"})
	}
	for _, id := range []string{"A", "V1", "V2"} {
		s.GrantObjectSee(id, actor.ID)
	}
	for _, to := range []string{"H1", "H2", "V1", "V2"} {
		if err := s.AddLink(Link{Type: lt, Source: "A", Target: to}); err != nil {
			t.Fatal(err)
		}
		// H 链接也不授予遍历权限（双重不可见），V 链接授予。
		if to[0] == 'V' {
			s.GrantLinkTraverse(Link{Type: lt, Source: "A", Target: to}, actor.ID)
		}
	}

	it := NewIterator(s, acl)
	got, trunc := collectAll(t, it, "A", 1, 2, actor)
	if fmt.Sprint(ids(got)) != "[A V1 V2]" {
		t.Fatalf("order = %v", ids(got))
	}
	if trunc != TruncComplete {
		t.Fatalf("invisible objects consumed fanout: trunc=%v", trunc)
	}
}

// TestStartDeletedDuringPagination：续读期间起点被删除 => ErrTokenObsolete。
func TestStartDeletedDuringPagination(t *testing.T) {
	s, acl, actor := buildGraph(t)
	for _, id := range []string{"A", "B", "C"} {
		addObject(s, acl, actor.ID, id)
	}
	addLink(s, acl, actor.ID, "A", "B")
	addLink(s, acl, actor.ID, "A", "C")

	it := NewIterator(s, acl)
	it.SetPageSize(1)
	p1, err := it.Traverse("A", 2, 10, "", actor)
	if err != nil {
		t.Fatal(err)
	}

	s.DeleteObject("A")

	_, err = it.Traverse("A", 2, 10, p1.NextToken, actor)
	if !errors.Is(err, ErrTokenObsolete) {
		t.Fatalf("want ErrTokenObsolete, got %v", err)
	}

	// 起点被删除期间，错误必须区别于“受限不可遍历”。
	if errors.Is(err, ErrForbidden) {
		t.Fatalf("deleted start must not be reported as forbidden")
	}
}

// TestDegenerateDepthZeroFanoutZero：深度 0 与扇出 0 的退化情形。
func TestDegenerateDepthZeroFanoutZero(t *testing.T) {
	s, acl, actor := buildGraph(t)
	for _, id := range []string{"A", "B"} {
		addObject(s, acl, actor.ID, id)
	}
	addLink(s, acl, actor.ID, "A", "B")
	it := NewIterator(s, acl)

	// depth=0：只返回起点；存在可遍历后继 => 深度截断。
	got, trunc := collectAll(t, it, "A", 0, 10, actor)
	if fmt.Sprint(ids(got)) != "[A]" || trunc != TruncDepth {
		t.Fatalf("depth0: order=%v trunc=%v", ids(got), trunc)
	}

	// fanout=0：只返回起点；后继被扇出截断 => 扇出截断优先。
	got, trunc = collectAll(t, it, "A", 2, 0, actor)
	if fmt.Sprint(ids(got)) != "[A]" || trunc != TruncFanout {
		t.Fatalf("fanout0: order=%v trunc=%v", ids(got), trunc)
	}
}

// TestRejectionOrder：参数非法 > 无权限 > 标记失效。
func TestRejectionOrder(t *testing.T) {
	s, acl, actor := buildGraph(t)
	s.PutObject(Object{ID: "A"})
	it := NewIterator(s, acl)

	if _, err := it.Traverse("A", -1, 2, "", actor); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("neg depth: %v", err)
	}
	if _, err := it.Traverse("A", 1, -2, "", actor); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("neg fanout: %v", err)
	}
	if _, err := it.Traverse("A", 1, 2, "garbage", actor); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("bad token: %v", err)
	}
	if _, err := it.Traverse("A", 1, 2, "ont1.xx.yy", actor); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("bad token sig: %v", err)
	}
	if _, err := it.Traverse("A", 1, 2, "", actor); !errors.Is(err, ErrForbidden) {
		t.Fatalf("no see perm: %v", err)
	}
}

// TestExcludedNeverReturnsViaOtherParents：被扇出剪掉的节点不能从别的父节点复活。
func TestExcludedNeverReturnsViaOtherParents(t *testing.T) {
	s, acl, actor := buildGraph(t)
	// A -> B1,B2（fanout=1，剪掉 B2）；B1 -> B2 也不得让 B2 复活。
	for _, id := range []string{"A", "B1", "B2"} {
		addObject(s, acl, actor.ID, id)
	}
	addLink(s, acl, actor.ID, "A", "B1")
	addLink(s, acl, actor.ID, "A", "B2")
	addLink(s, acl, actor.ID, "B1", "B2")

	it := NewIterator(s, acl)
	got, trunc := collectAll(t, it, "A", 3, 1, actor)
	if fmt.Sprint(ids(got)) != "[A B1]" {
		t.Fatalf("order = %v", ids(got))
	}
	if trunc != TruncFanout {
		t.Fatalf("trunc = %v", trunc)
	}
}
