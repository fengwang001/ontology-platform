package erase

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"ontology/refs"
)

// recSpec 描述一条记录：id 与 owners（["_"] 表示无主）。
type recSpec struct {
	id     int64
	owners []string
}

// edgeSpec 描述一条边；holdUntil>0 时给 child 加保全。
type edgeSpec struct {
	child, parent int64
	policy        refs.Policy
	holdUntil     int64
}

type wantReport struct {
	deleted    []int64
	detached   []int64
	anonymized []int64
	unlinked   []Ref
}

type eraseCase struct {
	name    string
	recs    []recSpec
	edges   []edgeSpec
	holds   map[int64]int64
	subject string
	now     int64
	want    wantReport
	wantErr error
	bound   int64
}

func build(t *testing.T, tc eraseCase) *Executor {
	t.Helper()
	// 所有登记操作在 setupNow 完成；当存在 until == now 的边界用例时，
	// setupNow 提前 1 秒以满足 Hold 的 until 严格大于 now，擦除仍发生在 tc.now。
	setupNow := tc.now
	for _, e := range tc.edges {
		if e.holdUntil > 0 && e.holdUntil-1 < setupNow {
			setupNow = e.holdUntil - 1
		}
	}
	for _, until := range tc.holds {
		if until-1 < setupNow {
			setupNow = until - 1
		}
	}
	if setupNow < 0 {
		setupNow = 0
	}
	ex := New()
	for _, r := range tc.recs {
		var owners []string
		if len(r.owners) != 1 || r.owners[0] != "_" {
			owners = r.owners
		}
		if err := ex.Put(r.id, owners, setupNow); err != nil {
			t.Fatalf("put %d: %v", r.id, err)
		}
	}
	for _, e := range tc.edges {
		if err := ex.AddRef(e.child, e.parent, e.policy, setupNow); err != nil {
			t.Fatalf("addref %d->%d: %v", e.child, e.parent, err)
		}
		if e.holdUntil > 0 {
			if err := ex.Hold(e.child, e.holdUntil, setupNow); err != nil {
				t.Fatalf("hold %d: %v", e.child, err)
			}
		}
	}
	for id, until := range tc.holds {
		if err := ex.Hold(id, until, setupNow); err != nil {
			t.Fatalf("hold %d: %v", id, err)
		}
	}
	return ex
}

func normInts(xs []int64) []int64 {
	if xs == nil {
		return []int64{}
	}
	return xs
}

func reportEqual(got *Report, w wantReport) bool {
	normRefs := func(xs []Ref) []Ref {
		if xs == nil {
			return []Ref{}
		}
		return xs
	}
	return reflect.DeepEqual(normInts(got.Deleted), normInts(w.deleted)) &&
		reflect.DeepEqual(normInts(got.Detached), normInts(w.detached)) &&
		reflect.DeepEqual(normInts(got.Anonymized), normInts(w.anonymized)) &&
		reflect.DeepEqual(normRefs(got.Unlinked), normRefs(w.unlinked))
}

func reportString(w wantReport) string {
	return fmt.Sprintf("deleted=%v detached=%v anonymized=%v unlinked=%v",
		w.deleted, w.detached, w.anonymized, w.unlinked)
}

// scenarioRecs 为题目例子的记录集合，便于复用。
var scenarioRecs = []recSpec{
	{1, []string{"s"}},
	{2, []string{"s", "t"}},
	{3, []string{"_"}},
	{4, []string{"t"}},
	{5, []string{"s"}},
	{6, []string{"t"}},
	{7, []string{"s"}},
}

var eraseCases = []eraseCase{
	{
		name: "shared owner record only detached",
		recs: []recSpec{{1, []string{"s", "t"}}, {2, []string{"s"}}},
		edges: []edgeSpec{
			{2, 1, refs.Cascade, 0},
		},
		subject: "s", now: 50,
		want: wantReport{
			deleted:    []int64{2},
			detached:   []int64{1},
			anonymized: []int64{},
			unlinked:   []Ref{},
		},
	},
	{
		name: "cascade child owned by other only unlinked",
		recs: []recSpec{{1, []string{"s"}}, {4, []string{"t"}}},
		edges: []edgeSpec{
			{4, 1, refs.Cascade, 0},
		},
		subject: "s", now: 50,
		want: wantReport{
			deleted:  []int64{1},
			unlinked: []Ref{{4, 1}},
		},
	},
	{
		name: "ownerless child cascades",
		recs: []recSpec{{1, []string{"s"}}, {3, []string{"_"}}},
		edges: []edgeSpec{
			{3, 1, refs.Cascade, 0},
		},
		subject: "s", now: 50,
		want: wantReport{
			deleted: []int64{1, 3},
		},
	},
	{
		name:    "hold exact until equals now expires",
		recs:    []recSpec{{1, []string{"s"}}},
		holds:   map[int64]int64{1: 100},
		subject: "s", now: 100,
		want: wantReport{deleted: []int64{1}},
	},
	{
		name:    "hold one second before anonymizes",
		recs:    []recSpec{{1, []string{"s"}}},
		holds:   map[int64]int64{1: 100},
		subject: "s", now: 99,
		want: wantReport{anonymized: []int64{1}},
	},
	{
		name: "anonymized seed edge into D unlinked",
		recs: []recSpec{{1, []string{"s"}}, {5, []string{"s"}}, {2, []string{"t"}}},
		edges: []edgeSpec{
			{5, 1, refs.Cascade, 100},
			{5, 2, refs.SetNull, 0},
		},
		subject: "s", now: 50,
		want: wantReport{
			deleted:    []int64{1},
			anonymized: []int64{5},
			unlinked:   []Ref{{5, 1}},
		},
	},
	{
		name: "restrict referrer itself in D does not block",
		recs: []recSpec{{1, []string{"s"}}, {7, []string{"s"}}, {3, []string{"_"}}},
		edges: []edgeSpec{
			{3, 1, refs.Cascade, 0},
			{7, 3, refs.Restrict, 0},
		},
		subject: "s", now: 50,
		want: wantReport{deleted: []int64{1, 3, 7}},
	},
	{
		name: "cascade cycle",
		recs: []recSpec{{1, []string{"s"}}, {8, []string{"_"}}, {9, []string{"_"}}},
		edges: []edgeSpec{
			{8, 1, refs.Cascade, 0},
			{9, 8, refs.Cascade, 0},
			{8, 9, refs.Cascade, 0},
		},
		subject: "s", now: 50,
		want: wantReport{deleted: []int64{1, 8, 9}},
	},
	{
		name:    "restricted blocks with minimal pair and zero change",
		recs:    scenarioRecs,
		edges:   scenarioRestrictEdges(),
		subject: "s", now: 50,
		wantErr: ErrRestricted,
		bound:   8,
	},
	{
		name:    "setnull instead of restricted succeeds",
		recs:    scenarioRecs,
		edges:   scenarioSetNullEdges(),
		subject: "s", now: 50,
		want: wantReport{
			deleted:    []int64{1, 3, 7},
			detached:   []int64{2},
			anonymized: []int64{5},
			unlinked:   []Ref{{4, 1}, {5, 1}, {6, 3}},
		},
		bound: 8,
	},
	{
		name: "hold expired at now equals deleted",
		recs: []recSpec{
			{1, []string{"s"}}, {3, []string{"_"}}, {4, []string{"t"}},
			{5, []string{"s"}}, {6, []string{"t"}},
		},
		edges: []edgeSpec{
			{3, 1, refs.Cascade, 0},
			{4, 1, refs.Cascade, 0},
			{5, 1, refs.Cascade, 100},
			{6, 3, refs.SetNull, 0},
		},
		subject: "s", now: 100,
		want: wantReport{
			deleted:  []int64{1, 3, 5},
			unlinked: []Ref{{4, 1}, {6, 3}},
		},
	},
	{
		name:    "empty graph erased writes tombstone",
		subject: "s", now: 50,
		want: wantReport{
			deleted:    []int64{},
			detached:   []int64{},
			anonymized: []int64{},
			unlinked:   []Ref{},
		},
	},
}

func scenarioRestrictEdges() []edgeSpec {
	return []edgeSpec{
		{3, 1, refs.Cascade, 0},
		{4, 1, refs.Cascade, 0},
		{5, 1, refs.Cascade, 100},
		{6, 3, refs.Restrict, 0},
		{7, 2, refs.SetNull, 0},
	}
}

func scenarioSetNullEdges() []edgeSpec {
	return []edgeSpec{
		{3, 1, refs.Cascade, 0},
		{4, 1, refs.Cascade, 0},
		{5, 1, refs.Cascade, 100},
		{6, 3, refs.SetNull, 0},
		{7, 2, refs.SetNull, 0},
	}
}

func TestEraseCases(t *testing.T) {
	for _, tc := range eraseCases {
		t.Run(tc.name, func(t *testing.T) {
			ex := build(t, tc)

			planRep, planErr := ex.Plan(tc.subject, tc.now)
			if tc.wantErr != nil {
				if !errors.Is(planErr, tc.wantErr) {
					t.Fatalf("plan err = %v, want %v", planErr, tc.wantErr)
				}
			} else if planErr != nil {
				t.Fatalf("plan unexpected err %v", planErr)
			} else if !reportEqual(planRep, tc.want) {
				t.Fatalf("plan report mismatch: got %+v want %s", planRep, reportString(tc.want))
			}

			before := snapshot(ex)
			rep, err := ex.Erase(tc.subject, tc.now)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("erase err = %v, want %v", err, tc.wantErr)
				}
				var re *RestrictedError
				if errors.As(err, &re) {
					if tc.name == "restricted blocks with minimal pair and zero change" &&
						!(re.Parent == 3 && re.Child == 6) {
						t.Fatalf("restricted pair = (%d,%d), want (3,6)", re.Parent, re.Child)
					}
					t.Logf("blocked by Restrict: parent=%d child=%d", re.Parent, re.Child)
				}
				if after := snapshot(ex); after != before {
					t.Fatalf("rejected erase changed state:\nbefore %s\nafter  %s", before, after)
				}
				return
			}
			if err != nil {
				t.Fatalf("erase unexpected err %v", err)
			}
			if !reportEqual(rep, tc.want) {
				t.Fatalf("erase report mismatch: got deleted=%v detached=%v anon=%v unlinked=%v\nwant %s",
					rep.Deleted, rep.Detached, rep.Anonymized, rep.Unlinked, reportString(tc.want))
			}
			if tc.bound > 0 && ex.visited > tc.bound {
				t.Fatalf("visited=%d exceeds bound %d", ex.visited, tc.bound)
			}
			t.Logf("OK: %s (visited=%d)", reportString(tc.want), ex.visited)
		})
	}
}

// snapshot 序列化执行器对外可观察的记录/边状态，用于零变化与终态校验。
func snapshot(ex *Executor) string {
	var b strings.Builder
	for _, id := range ex.store.IDs() {
		r, _ := ex.store.Get(id)
		owners := append([]string(nil), r.Owners...)
		sort.Strings(owners)
		fmt.Fprintf(&b, "r%d:%v:a%v:h%v;", id, owners, r.Anon, ex.store.Held(id, 50))
	}
	for _, e := range ex.graph.All() {
		fmt.Fprintf(&b, "e%d>%d=%d;", e.Child, e.Parent, e.Policy)
	}
	return b.String()
}

func TestTombstone(t *testing.T) {
	ex := New()
	if err := ex.Put(1, []string{"s"}, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := ex.Erase("s", 20); err != nil {
		t.Fatal(err)
	}
	if _, err := ex.Erase("s", 30); !errors.Is(err, ErrAlreadyErased) {
		t.Fatalf("second erase err = %v, want ErrAlreadyErased", err)
	}
	if _, err := ex.Plan("s", 30); !errors.Is(err, ErrAlreadyErased) {
		t.Fatalf("plan after erase err = %v, want ErrAlreadyErased", err)
	}
	if err := ex.Put(2, []string{"t", "s"}, 30); !errors.Is(err, ErrErased) {
		t.Fatalf("put with erased owner err = %v, want ErrErased", err)
	}
	if err := ex.Put(2, []string{"t"}, 30); err != nil {
		t.Fatalf("put with clean owner should succeed: %v", err)
	}
	t.Log("tombstone blocks re-erase, plan and Put owners; clean Put still works")
}

func TestRejectionOrder(t *testing.T) {
	ex := New()
	must := func(err error, want error, ctx string) {
		t.Helper()
		if !errors.Is(err, want) {
			t.Fatalf("%s: err = %v, want %v", ctx, err, want)
		}
	}

	// Put：参数非法 > 时钟回退 > 已存在 > ErrErased
	must(ex.Put(0, []string{"s"}, 5), ErrInvalid, "Put bad id")
	must(ex.Put(1, []string{"s", "s"}, 5), ErrInvalid, "Put dup owners")
	must(ex.Put(1, []string{"s", "t", "u", "v", "w"}, 5), ErrInvalid, "Put too many owners")
	must(ex.Put(1, []string{""}, 5), ErrInvalid, "Put empty subject name")
	if err := ex.Put(1, []string{"s"}, 10); err != nil {
		t.Fatal(err)
	}
	must(ex.Put(1, []string{"t"}, 5), ErrClockRollback, "Put rollback before exists")
	must(ex.Put(1, []string{"t"}, 10), ErrExists, "Put exists before erased check")
	if _, err := ex.Erase("s", 20); err != nil {
		t.Fatal(err)
	}
	must(ex.Put(2, []string{"s"}, 15), ErrClockRollback, "Put rollback before erased")
	must(ex.Put(2, []string{"s"}, 20), ErrErased, "Put erased owner")

	// AddRef：参数非法 > 时钟回退 > 记录不存在 > 已存在 > 出边超限
	must(ex.AddRef(1, 1, refs.Cascade, 30), ErrInvalid, "AddRef self loop")
	must(ex.AddRef(2, 3, refs.Policy(9), 30), ErrInvalid, "AddRef bad policy")
	must(ex.AddRef(90, 91, refs.Cascade, 19), ErrClockRollback, "AddRef rollback")
	must(ex.AddRef(90, 91, refs.Cascade, 30), ErrNotFound, "AddRef missing endpoints")
	if err := ex.Put(10, []string{"t"}, 30); err != nil {
		t.Fatal(err)
	}
	if err := ex.Put(11, []string{"t"}, 30); err != nil {
		t.Fatal(err)
	}
	if err := ex.AddRef(10, 11, refs.Cascade, 30); err != nil {
		t.Fatal(err)
	}
	must(ex.AddRef(10, 11, refs.Restrict, 30), ErrExists, "AddRef exists")
	for i := int64(0); i < 8; i++ {
		parent := int64(100 + i)
		if err := ex.Put(parent, []string{"t"}, 30); err != nil {
			t.Fatal(err)
		}
	}
	// 10 已有 1 条出边，再补 7 条达上限 8，第 9 条报超限（而非已存在）。
	for i := int64(0); i < 7; i++ {
		if err := ex.AddRef(10, 100+i, refs.Cascade, 30); err != nil {
			t.Fatal(err)
		}
	}
	must(ex.AddRef(10, 107, refs.Cascade, 30), ErrTooManyRefs, "AddRef out-degree")

	// RemoveRef：参数非法 > 时钟回退 > 边不存在
	must(ex.RemoveRef(10, 10, 30), ErrInvalid, "RemoveRef self loop")
	must(ex.RemoveRef(10, 11, 25), ErrClockRollback, "RemoveRef rollback")
	must(ex.RemoveRef(90, 91, 30), ErrNotFound, "RemoveRef missing")

	// Hold：参数非法 > 时钟回退 > 记录不存在
	must(ex.Hold(10, 30, 30), ErrInvalid, "Hold until == now")
	must(ex.Hold(10, 40, 25), ErrClockRollback, "Hold rollback")
	must(ex.Hold(90, 40, 30), ErrNotFound, "Hold missing record")

	// Erase/Plan：参数非法 > 时钟回退 > ErrAlreadyErased > ErrRestricted
	if _, err := ex.Erase("", 30); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Erase empty subject err = %v", err)
	}
	if _, err := ex.Plan("s", 10); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("Plan rollback err = %v", err)
	}
	if _, err := ex.Erase("s", 30); !errors.Is(err, ErrAlreadyErased) {
		t.Fatalf("Erase tombstoned err = %v", err)
	}
	t.Log("rejection order verified for Put/AddRef/RemoveRef/Hold/Erase/Plan")
}

// TestVisitedIndependentOfUnrelatedRecords 验证 visited 与全库无关记录数脱钩：
// 同一擦除计划在 100 与 10000 条无关记录两档下 visited 完全相同且不超过上界。
func TestVisitedIndependentOfUnrelatedRecords(t *testing.T) {
	buildWorld := func(unrelated int) (*Executor, int64) {
		ex := New()
		now := int64(10)
		// 核心世界：1(s) -> 3(_) Cascade，4(t) Cascade->1。
		if err := ex.Put(1, []string{"s"}, now); err != nil {
			t.Fatal(err)
		}
		if err := ex.Put(3, nil, now); err != nil {
			t.Fatal(err)
		}
		if err := ex.Put(4, []string{"t"}, now); err != nil {
			t.Fatal(err)
		}
		if err := ex.AddRef(3, 1, refs.Cascade, now); err != nil {
			t.Fatal(err)
		}
		if err := ex.AddRef(4, 1, refs.Cascade, now); err != nil {
			t.Fatal(err)
		}
		// 无关记录自成密集小图，不接触 D。
		for i := 0; i < unrelated; i++ {
			id := int64(1000 + i)
			if err := ex.Put(id, []string{"u"}, now); err != nil {
				t.Fatal(err)
			}
			if i > 0 {
				prev := int64(1000 + i - 1)
				if err := ex.AddRef(id, prev, refs.Cascade, now); err != nil {
					t.Fatal(err)
				}
			}
		}
		// 上界 = 种子数(1) + indeg(1)+indeg(3) = 1 + 1 + 1 = 3。
		return ex, 3
	}

	ex100, bound := buildWorld(100)
	if _, err := ex100.Plan("s", 10); err != nil {
		t.Fatal(err)
	}
	v100 := ex100.Visited()

	ex10000, _ := buildWorld(10000)
	if _, err := ex10000.Plan("s", 10); err != nil {
		t.Fatal(err)
	}
	v10000 := ex10000.Visited()

	t.Logf("visited unrelated=100: %d; unrelated=10000: %d; bound=%d", v100, v10000, bound)
	if v100 != v10000 {
		t.Fatalf("visited depends on unrelated records: %d vs %d", v100, v10000)
	}
	if v100 > bound {
		t.Fatalf("visited %d exceeds bound %d", v100, bound)
	}
}

func TestConcurrentEquivalentToSerial(t *testing.T) {
	ex := New()
	now := int64(1)
	if err := ex.Put(1, []string{"s"}, now); err != nil {
		t.Fatal(err)
	}
	for i := 2; i <= 50; i++ {
		if err := ex.Put(int64(i), []string{"s"}, now); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for i := 2; i <= 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = ex.Hold(int64(i), 500, now)
			_ = ex.AddRef(int64(i), 1, refs.SetNull, now)
		}(i)
	}
	wg.Wait()
	rep, err := ex.Erase("s", 100)
	if err != nil {
		t.Fatalf("concurrent erase: %v", err)
	}
	t.Logf("concurrent setup then erase: deleted=%v anonymized=%v unlinked=%d",
		rep.Deleted, rep.Anonymized, len(rep.Unlinked))
	for _, id := range ex.store.IDs() {
		r, _ := ex.store.Get(id)
		if len(r.Owners) != 0 {
			t.Fatalf("record %d still owned by %v after erase", id, r.Owners)
		}
	}
	for _, e := range ex.graph.All() {
		for _, d := range rep.Deleted {
			if e.Parent == d || e.Child == d {
				t.Fatalf("edge %d->%d still touches deleted %d", e.Child, e.Parent, d)
			}
		}
	}
}

func TestUnlinkedSortedByChildParent(t *testing.T) {
	// D={10,2}；跨边 (3->10) 与 (4->2)：按 child 排序为 (3,10),(4,2)，
	// 若误按 parent 排序会得到 (4,2),(3,10)。
	ex := New()
	now := int64(10)
	for _, spec := range []recSpec{
		{2, []string{"s"}}, {10, []string{"s"}},
		{3, []string{"t"}}, {4, []string{"t"}},
	} {
		if err := ex.Put(spec.id, spec.owners, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := ex.AddRef(3, 10, refs.SetNull, now); err != nil {
		t.Fatal(err)
	}
	if err := ex.AddRef(4, 2, refs.SetNull, now); err != nil {
		t.Fatal(err)
	}
	rep, err := ex.Erase("s", now)
	if err != nil {
		t.Fatal(err)
	}
	want := []Ref{{Child: 3, Parent: 10}, {Child: 4, Parent: 2}}
	if !reflect.DeepEqual(rep.Unlinked, want) {
		t.Fatalf("unlinked = %v, want %v (must sort by child then parent)", rep.Unlinked, want)
	}
	t.Logf("unlinked order OK: %v", rep.Unlinked)
}

// TestRestrictMinimalPairOrdering 验证多 parent 各有 Restrict 引用者时
// 取被引 D 内最小 parent，再取其最小 Restrict child。
func TestRestrictMinimalPairOrdering(t *testing.T) {
	ex := New()
	now := int64(10)
	for _, spec := range []recSpec{
		{2, []string{"s"}}, {10, []string{"s"}},
		{3, []string{"t"}}, {4, []string{"t"}}, {5, []string{"t"}},
	} {
		if err := ex.Put(spec.id, spec.owners, now); err != nil {
			t.Fatal(err)
		}
	}
	// parent=2 的 restrict child 为 5；parent=10 的 restrict child 为 3、4。
	// 最小 parent=2 -> pair (2,5)，即使 (3,10) 的 child 更小也不选它。
	if err := ex.AddRef(5, 2, refs.Restrict, now); err != nil {
		t.Fatal(err)
	}
	if err := ex.AddRef(3, 10, refs.Restrict, now); err != nil {
		t.Fatal(err)
	}
	if err := ex.AddRef(4, 10, refs.Restrict, now); err != nil {
		t.Fatal(err)
	}
	_, err := ex.Erase("s", now)
	var re *RestrictedError
	if !errors.As(err, &re) {
		t.Fatalf("err = %v, want RestrictedError", err)
	}
	if re.Parent != 2 || re.Child != 5 {
		t.Fatalf("restricted pair = (%d,%d), want (2,5): parent-min takes priority", re.Parent, re.Child)
	}
	t.Logf("minimal restricted pair OK: parent=%d child=%d", re.Parent, re.Child)
}
