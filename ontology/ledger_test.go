package ontology

import (
	"fmt"
	"testing"
)

// testEnv 搭建一个最小但有代表性的本体：
// Person -authored-> Article，起点侧至多 2，终点侧恰好一。
type testEnv struct {
	ledger   *Ledger
	imp      *Importer
	persons  []InstanceID
	articles []InstanceID
}

func setup(t *testing.T) *testEnv {
	t.Helper()
	l := NewLedger()
	must(t, l.RegisterLinkType(LinkTypeSpec{
		Name:        "authored",
		SourceType:  "Person",
		TargetType:  "Article",
		SourceBound: AtMost(2),
		TargetBound: ExactlyOne(),
	}))
	env := &testEnv{ledger: l, imp: NewImporter(l)}
	for _, id := range []InstanceID{"p1", "p2", "p3"} {
		must(t, l.CreateObject("Person", id))
		env.persons = append(env.persons, id)
	}
	for _, id := range []InstanceID{"a1", "a2", "a3", "a4", "a5"} {
		must(t, l.CreateObject("Article", id))
		env.articles = append(env.articles, id)
	}
	return env
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func reasonOf(err error) Reason {
	if le, ok := AsLinkError(err); ok {
		return le.Code
	}
	return ""
}

func TestErrorCategoriesAreDistinct(t *testing.T) {
	env := setup(t)
	l := env.ledger

	// 参数非法：实例不存在。
	err := l.CreateLink("authored", "ghost", "a1")
	logCase(t, "create(ghost->a1)", err, "expect invalid_argument: source instance missing")
	if reasonOf(err) != ReasonInvalidArgument {
		t.Fatalf("got %v", err)
	}

	// 删除不存在的链接：独立类别，绝不是基数超限。
	err = l.DeleteLink("authored", "p1", "a1")
	logCase(t, "delete(p1->a1) absent", err, "expect link_not_found")
	if reasonOf(err) != ReasonLinkNotFound {
		t.Fatalf("got %v", err)
	}

	// 制造终点侧已满（恰好一），验证删除不存在与基数已满可区分。
	must(t, l.CreateLink("authored", "p1", "a1"))
	err = l.CreateLink("authored", "p2", "a1")
	logCase(t, "create(p2->a1) target full", err, "expect target_cardinality_exceeded")
	if reasonOf(err) != ReasonTargetCardinality {
		t.Fatalf("got %v", err)
	}
}

func TestCardinalityBoundaryAtLimitAndOneOver(t *testing.T) {
	env := setup(t)
	l := env.ledger

	// 起点 p1 上限 2：第 1、2 条成功（恰好等于上限），第 3 条以「超出一个」被拒。
	must(t, l.CreateLink("authored", "p1", "a1"))
	must(t, l.CreateLink("authored", "p1", "a2"))
	got := l.Occupied("authored", SideSource, "p1")
	t.Logf("input p1 authored twice; actual source occupancy=%d; basis limit=%d => at-limit accepted", got, 2)
	if got != 2 {
		t.Fatalf("occupancy = %d, want 2", got)
	}
	err := l.CreateLink("authored", "p1", "a3")
	logCase(t, "create(p1->a3) one over", err, "expect source_cardinality_exceeded")
	if reasonOf(err) != ReasonSourceCardinality {
		t.Fatalf("got %v", err)
	}
	// 被拒绝者不得留痕、不占名额。
	if l.HasLink("authored", "p1", "a3") || l.Occupied("authored", SideTarget, "a3") != 0 {
		t.Fatal("rejected link left a trace")
	}
}

func TestErrorOrderingSourceBeforeTarget(t *testing.T) {
	env := setup(t)
	l := env.ledger
	// p1 起点侧填满 2，a1 终点侧填 1；下一条两端同时超限，必须先报起点。
	must(t, l.CreateLink("authored", "p1", "a2"))
	must(t, l.CreateLink("authored", "p1", "a3"))
	must(t, l.CreateLink("authored", "p2", "a1"))
	err := l.CreateLink("authored", "p1", "a1")
	logCase(t, "create(p1->a1) both ends full", err, "expect source reason first")
	if reasonOf(err) != ReasonSourceCardinality {
		t.Fatalf("got %v", err)
	}

	// 参数非法优先于一切基数结论：p1 起点已满，但 ghost 不存在，先报参数。
	err = l.CreateLink("authored", "ghost", "a1")
	logCase(t, "create(ghost->a1) with p-side/a1 full", err, "expect invalid_argument first")
	if reasonOf(err) != ReasonInvalidArgument {
		t.Fatalf("got %v", err)
	}
}

func TestBatchCumulativeOccupancy(t *testing.T) {
	env := setup(t)
	// 单看每条都合法：p1 起点侧至多 2，但批次三条都从 p1 出发，
	// 累积占用使第 3 条被拒——这正是「不基于批次前快照独立判断」的要求。
	pairs := []InstancePair{
		{Source: "p1", Target: "a1"},
		{Source: "p1", Target: "a2"},
		{Source: "p1", Target: "a3"},
	}

	report := env.imp.Import("authored", pairs, ModeBestEffort)
	t.Logf("BEST-EFFORT input=%v", pairs)
	for _, r := range report.Results {
		basis := "accepted"
		if r.Err != nil {
			basis = string(r.Err.Code)
		}
		t.Logf("  index=%d pair=(%s,%s) status=%s basis=%s", r.Index, r.Pair.Source, r.Pair.Target, r.Status, basis)
	}
	if report.Accepted != 2 || report.Results[2].Err.Code != ReasonSourceCardinality {
		t.Fatalf("cumulative occupancy not enforced: %+v", report.Results)
	}
}

func TestAllOrNothingVersusBestEffort(t *testing.T) {
	pairs := []InstancePair{
		{Source: "p1", Target: "a1"},
		{Source: "p1", Target: "a2"},
		{Source: "p1", Target: "a3"}, // 起点侧超限：累积第 3 条
		{Source: "p2", Target: "a4"}, // 本身合法
	}

	// 全有或全无：全部撤销，链接存储为空。
	envA := setup(t)
	reportA := envA.imp.Import("authored", pairs, ModeAllOrNothing)
	t.Logf("ALL-OR-NOTHING input=%v committed=%v", pairs, reportA.Committed)
	for _, r := range reportA.Results {
		t.Logf("  index=%d status=%s err=%v", r.Index, r.Status, r.Err)
	}
	if reportA.Committed || envA.ledger.LinkCount() != 0 {
		t.Fatalf("all-or-nothing leaked %d links", envA.ledger.LinkCount())
	}
	if reportA.Results[0].Status != StatusRolledBack ||
		reportA.Results[2].Status != StatusRejected ||
		reportA.Results[3].Status != StatusSkipped {
		t.Fatalf("unexpected statuses: %+v", reportA.Results)
	}

	// 尽力而为：前两条与第 4 条生效，第 3 条拒绝。
	envB := setup(t)
	reportB := envB.imp.Import("authored", pairs, ModeBestEffort)
	t.Logf("BEST-EFFORT input=%v committed=%v accepted=%d", pairs, reportB.Committed, reportB.Accepted)
	for _, r := range reportB.Results {
		t.Logf("  index=%d status=%s err=%v", r.Index, r.Status, r.Err)
	}
	if !reportB.Committed || reportB.Accepted != 3 || envB.ledger.LinkCount() != 3 {
		t.Fatalf("best-effort result wrong: accepted=%d links=%d", reportB.Accepted, envB.ledger.LinkCount())
	}
	if reportB.Results[2].Status != StatusRejected || reportB.Results[3].Status != StatusAccepted {
		t.Fatalf("unexpected best-effort statuses: %+v", reportB.Results)
	}

	// 同一输入两种语义结果必须不同：0 条生效 vs 3 条生效。
	if envA.ledger.LinkCount() == envB.ledger.LinkCount() {
		t.Fatal("two modes must differ on the same input")
	}
}

func TestBatchDuplicatePairIsInvalidArgument(t *testing.T) {
	pairs := []InstancePair{
		{Source: "p2", Target: "a1"},
		{Source: "p2", Target: "a1"},
	}
	for _, mode := range []ImportMode{ModeAllOrNothing, ModeBestEffort} {
		env := setup(t)
		report := env.imp.Import("authored", pairs, mode)
		t.Logf("mode=%v input=%v results=%+v", mode, pairs, report.Results)
		if report.Results[1].Err == nil || report.Results[1].Err.Code != ReasonInvalidArgument {
			t.Fatalf("duplicate pair must be invalid_argument, got %+v", report.Results[1])
		}
		if mode == ModeBestEffort && report.Accepted != 1 {
			t.Fatalf("first occurrence should be accepted, accepted=%d", report.Accepted)
		}
		if mode == ModeAllOrNothing && env.ledger.LinkCount() != 0 {
			t.Fatal("all-or-nothing duplicate batch must leave no link")
		}
	}
}

func TestDeleteReleasesImmediately(t *testing.T) {
	env := setup(t)
	l := env.ledger
	must(t, l.CreateLink("authored", "p1", "a1"))
	must(t, l.DeleteLink("authored", "p1", "a1"))
	// 释放后同名额立刻可被再次占用：同锁串行，无任何额外时延。
	must(t, l.CreateLink("authored", "p2", "a1"))
	got := l.Occupied("authored", SideTarget, "a1")
	t.Logf("after delete then create: a1 target occupancy=%d basis limit=1", got)
	if got != 1 || l.LinkCount() != 1 {
		t.Fatalf("release not immediately visible: occ=%d links=%d", got, l.LinkCount())
	}
}

func logCase(t *testing.T, input string, err error, basis string) {
	t.Helper()
	t.Logf("input=%s actual_err=%v basis=%s", input, err, basis)
}

var _ = fmt.Sprint
