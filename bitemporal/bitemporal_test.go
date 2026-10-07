package bitemporal

import (
	"context"
	"testing"
)

func regTypes(t *testing.T, st *Store, sym bool) ID {
	t.Helper()
	must(t, st.RegisterObjectType("T", 0))
	if !sym {
		must(t, st.RegisterObjectType("U", 0))
	}
	lt := LinkType{ID: "L", SourceType: "T", TargetType: "U", Symmetric: false}
	if sym {
		lt = LinkType{ID: "L", SourceType: "T", TargetType: "T", Symmetric: true}
	}
	must(t, st.RegisterLinkType(lt, Cardinality{Forward: Card{Min: 0, Max: 0}, Reverse: Card{Min: 0, Max: 0}}, 0))
	return "L"
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func edgeSet(es []Edge) map[[2]ID]bool {
	m := map[[2]ID]bool{}
	for _, e := range es {
		m[[2]ID{e.From, e.To}] = true
	}
	return m
}

func TestSmokeAsymmetricReplay(t *testing.T) {
	st := NewStore()
	regTypes(t, st, false)
	must(t, st.RecordCreate("L", "a", "b", 10, 10))
	must(t, st.RecordRevoke("L", "a", "b", 30, 40))
	snap := st.CurrentSnapshot()

	if got := snap.Replay("L", 5, 5).Edges; len(got) != 0 {
		t.Fatalf("expected no edges at rt=5, got %v", got)
	}
	r := snap.Replay("L", 20, 20)
	if len(r.Edges) != 1 || r.Edges[0].From != "a" || r.Edges[0].To != "b" {
		t.Fatalf("expected single edge a->b at 20, got %v", r.Edges)
	}
	fwd, rev, defects := snap.CheckMirror("L", 20, 20)
	if len(defects) != 0 || len(fwd) != 1 || len(rev) != 1 {
		t.Fatalf("mirror mismatch: fwd=%v rev=%v defects=%v", fwd, rev, defects)
	}
	if rev[0].From != "b" || rev[0].To != "a" {
		t.Fatalf("reverse mirror wrong: %v", rev[0])
	}
	// 撤销在记录时间 40 才被知晓：rt=35 时，回看有效时间 20 的世界，关系仍存在；
	// 而有效时间 30 的世界在 rt=40 才知道撤销已发生。
	if got := snap.Replay("L", 35, 20).Edges; len(got) != 1 {
		t.Fatalf("edge should still exist at (rt=35,vt=20), got %v", got)
	}
	if got := snap.Replay("L", 35, 30).Edges; len(got) != 1 {
		t.Fatalf("before revoke is known (rt=35), edge must still replay at vt=30, got %v", got)
	}
	// rt=40 起撤销才被知晓。
	if got := snap.Replay("L", 40, 30).Edges; len(got) != 0 {
		t.Fatalf("edge must be revoked once known at (rt=40,vt=30), got %v", got)
	}
	if got := snap.Replay("L", 40, 30).Edges; len(got) != 0 {
		t.Fatalf("edge should be revoked at (40,30), got %v", got)
	}
}

func TestSmokeSymmetric(t *testing.T) {
	st := NewStore()
	regTypes(t, st, true)
	// 只从一端记录一次创建。
	must(t, st.RecordCreate("L", "a", "b", 10, 10))
	snap := st.CurrentSnapshot()
	r := snap.Replay("L", 20, 20)
	if len(r.Edges) != 2 || len(r.Defects) != 0 {
		t.Fatalf("symmetric edge must show both directions, got edges=%v defects=%v", r.Edges, r.Defects)
	}
	es := edgeSet(r.Edges)
	if !es[[2]ID{"a", "b"}] || !es[[2]ID{"b", "a"}] {
		t.Fatalf("missing symmetric directions: %v", es)
	}

	// 历史遗留：仅 ba 半有创建，应显式报结构缺陷而非静默对称。
	st2 := NewStore()
	regTypes(t, st2, true)
	must(t, st2.IngestLegacyHalf("L", "a", "b", 10, 10, true, "ba"))
	snap2 := st2.CurrentSnapshot()
	r2 := snap2.Replay("L", 20, 20)
	if len(r2.Edges) != 0 || len(r2.Defects) != 1 {
		t.Fatalf("expected structural defect, got edges=%v defects=%v", r2.Edges, r2.Defects)
	}
	if r2.Defects[0].MissingSide != halfAB {
		t.Fatalf("missing side should be ab, got %q", r2.Defects[0].MissingSide)
	}

	aud := NewAuditor(st2, NewDecisionLog())
	_, err := aud.Audit(context.Background(), AuditRequest{LinkType: "L", RecordStart: 15, RecordEnd: 25})
	if err == nil {
		t.Fatalf("expected E4 structural error")
	}
	ae := err.(*AuditError)
	if ae.Kind != ErrMirrorStructural {
		t.Fatalf("expected ErrMirrorStructural, got %v", ae.Kind)
	}

	// 审计失败不得改变历史。
	if got := st2.CurrentSnapshot().Replay("L", 20, 20); len(got.Defects) != 1 || len(got.Edges) != 0 {
		t.Fatalf("history mutated by failed audit: %+v", got)
	}
}

func TestSmokeVersionSegmentation(t *testing.T) {
	st := NewStore()
	must(t, st.RegisterObjectType("T", 0))
	must(t, st.RegisterObjectType("U", 0))
	must(t, st.RegisterLinkType(LinkType{ID: "L", SourceType: "T", TargetType: "U"},
		Cardinality{Forward: Card{Max: 1}, Reverse: Card{Max: 1}}, 0))
	// a 同时关联 b 与 c：在 Max=1 规则下违反；规则在记录时间 50 放宽为 Max=2。
	must(t, st.RecordCreate("L", "a", "b", 10, 10))
	must(t, st.RecordCreate("L", "a", "c", 20, 20))
	must(t, st.PutRule("L", Cardinality{Forward: Card{Max: 2}, Reverse: Card{Max: 2}}, 50))

	aud := NewAuditor(st, NewDecisionLog())
	segs, err := aud.Audit(context.Background(), AuditRequest{LinkType: "L", RecordStart: 0, RecordEnd: 100})
	must(t, err)
	if len(segs) < 2 {
		t.Fatalf("expected segmented output across rule versions, got %d", len(segs))
	}
}
