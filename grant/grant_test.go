package grant

import (
	"errors"
	"fmt"
	"testing"

	"ontology/territory"
)

func mustTree(t *testing.T, children map[string][]string) *territory.Tree {
	t.Helper()
	tr, err := territory.NewTree(children)
	if err != nil {
		t.Fatalf("NewTree: %v", err)
	}
	return tr
}

func exampleTree(t *testing.T) *territory.Tree {
	return mustTree(t, map[string][]string{
		territory.Root: {"EU", "AS"},
		"EU":           {"FR", "DE"},
		"AS":           {"JP", "KR"},
	})
}

func bs(s string) []byte { return []byte(s) }

type addArgs struct {
	now                       int64
	id, title, licensee, node string
	excludes                  []string
	start, end                int64
	exclusive                 bool
}

func (r *Registry) mustAdd(t *testing.T, a addArgs) *Grant {
	t.Helper()
	g, err := r.Add(a.now, bs(a.id), bs(a.title), bs(a.licensee), a.node,
		append([]string(nil), a.excludes...), a.start, a.end, a.exclusive)
	if err != nil {
		t.Fatalf("Add(%s): %v", a.id, err)
	}
	return g
}

func TestAddValidationOrder(t *testing.T) {
	tr := exampleTree(t)
	cases := []struct {
		name string
		args addArgs
		want error
	}{
		{"start>=end", addArgs{now: 10, id: "x", title: "T", licensee: "A", node: "FR", start: 5, end: 5}, ErrInvalidArgument},
		{"time out of range", addArgs{now: 10, id: "x", title: "T", licensee: "A", node: "FR", start: 0, end: MaxTime + 1}, ErrInvalidArgument},
		{"empty id", addArgs{now: 10, id: "", title: "T", licensee: "A", node: "FR", start: 0, end: 1}, ErrInvalidArgument},
		{"too many excludes", addArgs{now: 10, id: "x", title: "T", licensee: "A", node: territory.Root, excludes: []string{"EU", "AS", "FR", "DE", "JP", "KR", "a", "b", "c"}, start: 0, end: 1}, ErrInvalidArgument},
		{"duplicate exclude", addArgs{now: 10, id: "x", title: "T", licensee: "A", node: "EU", excludes: []string{"FR", "FR"}, start: 0, end: 1}, ErrInvalidArgument},
		{"clock back", addArgs{now: 5, id: "x", title: "T", licensee: "A", node: "FR", start: 0, end: 1}, ErrClockMovedBack},
		{"duplicate id", addArgs{now: 10, id: "g1", title: "T", licensee: "A", node: "FR", start: 0, end: 1}, ErrDuplicateID},
		{"unknown node", addArgs{now: 10, id: "y", title: "T", licensee: "A", node: "MARS", start: 0, end: 1}, ErrUnknownNode},
		{"unknown exclude", addArgs{now: 10, id: "y", title: "T", licensee: "A", node: territory.Root, excludes: []string{"MARS"}, start: 0, end: 1}, ErrUnknownNode},
		{"exclude not descendant", addArgs{now: 10, id: "y", title: "T", licensee: "A", node: "EU", excludes: []string{"JP"}, start: 0, end: 1}, ErrInvalidExclude},
		{"exclude equals node", addArgs{now: 10, id: "y", title: "T", licensee: "A", node: "EU", excludes: []string{"EU"}, start: 0, end: 1}, ErrInvalidExclude},
		{"exclude ancestor pair", addArgs{now: 10, id: "y", title: "T", licensee: "A", node: territory.Root, excludes: []string{"EU", "FR"}, start: 0, end: 1}, ErrInvalidExclude},
		{"empty coverage", addArgs{now: 10, id: "y", title: "T", licensee: "A", node: "EU", excludes: []string{"FR", "DE"}, start: 0, end: 1}, ErrEmptyCoverage},
	}
	r := NewRegistry(tr)
	r.mustAdd(t, addArgs{now: 10, id: "g1", title: "T", licensee: "Z", node: "JP", start: 0, end: 100, exclusive: true})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := r.Add(tc.args.now, bs(tc.args.id), bs(tc.args.title), bs(tc.args.licensee),
				tc.args.node, append([]string(nil), tc.args.excludes...), tc.args.start, tc.args.end, tc.args.exclusive)
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
	if err := r.Revoke(9, bs("g1")); !errors.Is(err, ErrClockMovedBack) {
		t.Fatalf("clock must remain 10 after rejections, got %v", err)
	}
}

func TestExclusiveConflictsExample(t *testing.T) {
	tr := exampleTree(t)
	r := NewRegistry(tr)

	g1 := r.mustAdd(t, addArgs{now: 100, id: "g1", title: "T", licensee: "甲", node: "EU", excludes: []string{"FR"}, start: 100, end: 200, exclusive: true})
	if got := fmt.Sprint(g1.cover); got != "[{2 3}]" {
		t.Fatalf("g1 cover = %v, want only DE={2}", got)
	}
	r.mustAdd(t, addArgs{now: 100, id: "g2", title: "T", licensee: "乙", node: territory.Root, excludes: []string{"DE"}, start: 150, end: 300, exclusive: true})
	r.mustAdd(t, addArgs{now: 100, id: "g4", title: "T", licensee: "丙", node: "DE", start: 200, end: 250, exclusive: false})

	_, err := r.Add(100, bs("g5"), bs("T"), bs("丙"), "EU", nil, 199, 210, false)
	var ce ConflictError
	if !errors.As(err, &ce) || string(ce.Conflict().ID) != "g1" {
		t.Fatalf("g5 should conflict with byte-min g1, got %v", err)
	}

	// 同被授权方独占可与 g1 在 DE 上重叠；与 g4(丙) 仅端点 200 相接，不算相交
	r.mustAdd(t, addArgs{now: 100, id: "g6", title: "T", licensee: "甲", node: "DE", start: 0, end: 200, exclusive: true})
	// 不同方非独占可在无独占的 FR 上共存（g1 排除了 FR）
	r.mustAdd(t, addArgs{now: 100, id: "g7", title: "T", licensee: "丁", node: "FR", start: 100, end: 150, exclusive: false})
	r.mustAdd(t, addArgs{now: 100, id: "g8", title: "T", licensee: "戊", node: "FR", start: 100, end: 150, exclusive: false})
	_, err = r.Add(100, bs("g9"), bs("T"), bs("庚"), "FR", nil, 100, 150, true)
	if !errors.As(err, &ce) || string(ce.Conflict().ID) != "g7" {
		t.Fatalf("exclusive must conflict with non-exclusive g7, got %v", err)
	}
	r.mustAdd(t, addArgs{now: 100, id: "g10", title: "OTHER", licensee: "X", node: territory.Root, start: 0, end: 1000, exclusive: true})

	if err := r.Revoke(160, bs("g1")); err != nil {
		t.Fatal(err)
	}
	_, err = r.Add(160, bs("g5"), bs("T"), bs("丙"), "EU", nil, 199, 210, false)
	if !errors.As(err, &ce) || string(ce.Conflict().ID) != "g2" {
		t.Fatalf("after revoking g1, g5 should conflict with g2, got %v", err)
	}
}

func TestMinConflictID(t *testing.T) {
	tr := exampleTree(t)
	r := NewRegistry(tr)
	r.mustAdd(t, addArgs{now: 1, id: "z9", title: "T", licensee: "A", node: "JP", start: 0, end: 10, exclusive: false})
	r.mustAdd(t, addArgs{now: 1, id: "a2", title: "T", licensee: "B", node: "JP", start: 0, end: 10, exclusive: false})
	r.mustAdd(t, addArgs{now: 1, id: "m5", title: "T", licensee: "C", node: "JP", start: 0, end: 10, exclusive: false})
	_, err := r.Add(1, bs("new"), bs("T"), bs("D"), "JP", nil, 0, 10, true)
	var ce ConflictError
	if !errors.As(err, &ce) || string(ce.Conflict().ID) != "a2" {
		t.Fatalf("want conflict with byte-min a2, got %v", err)
	}
}

func TestRevokeBranches(t *testing.T) {
	tr := exampleTree(t)
	r := NewRegistry(tr)
	r.mustAdd(t, addArgs{now: 50, id: "g1", title: "T", licensee: "A", node: "JP", start: 100, end: 200, exclusive: true})

	if err := r.Revoke(200, bs("g1")); !errors.Is(err, ErrExpired) {
		t.Fatalf("at end want ErrExpired, got %v", err)
	}
	if err := r.Revoke(250, bs("g1")); !errors.Is(err, ErrExpired) {
		t.Fatalf("after end want ErrExpired, got %v", err)
	}
	if err := r.Revoke(100, bs("g1")); err != nil {
		t.Fatalf("revoke at start must delete: %v", err)
	}
	if r.Get(bs("g1")) != nil {
		t.Fatal("g1 should be deleted when now==start")
	}

	snap := r.mustAdd(t, addArgs{now: 100, id: "g2", title: "T", licensee: "A", node: "JP", start: 100, end: 200, exclusive: true})
	if err := r.Revoke(160, bs("g2")); err != nil {
		t.Fatal(err)
	}
	if snap.End != 200 {
		t.Fatal("returned snapshot must not change")
	}
	if got := r.Get(bs("g2")); got.End != 160 {
		t.Fatalf("end = %d, want 160", got.End)
	}

	if err := r.Revoke(-1, bs("g2")); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("got %v", err)
	}
	if err := r.Revoke(150, bs("g2")); !errors.Is(err, ErrClockMovedBack) {
		t.Fatalf("got %v", err)
	}
	if err := r.Revoke(160, bs("missing")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	if err := r.Revoke(160, bs("g2")); !errors.Is(err, ErrExpired) {
		t.Fatalf("end==now must be expired, got %v", err)
	}
}

func TestExtend(t *testing.T) {
	tr := exampleTree(t)
	r := NewRegistry(tr)
	r.mustAdd(t, addArgs{now: 100, id: "g1", title: "T", licensee: "甲", node: "EU", excludes: []string{"FR"}, start: 100, end: 200, exclusive: true})
	r.mustAdd(t, addArgs{now: 100, id: "g2", title: "T", licensee: "乙", node: territory.Root, excludes: []string{"DE"}, start: 150, end: 300, exclusive: true})
	r.mustAdd(t, addArgs{now: 100, id: "g4", title: "T", licensee: "丙", node: "DE", start: 200, end: 250, exclusive: false})

	if err := r.Revoke(160, bs("g1")); err != nil {
		t.Fatal(err)
	}
	if err := r.Extend(170, bs("g1"), 260); !errors.Is(err, ErrExpired) {
		t.Fatalf("truncated grant extended after end want ErrExpired, got %v", err)
	}

	r.mustAdd(t, addArgs{now: 170, id: "g1b", title: "T", licensee: "甲", node: "EU", excludes: []string{"FR"}, start: 100, end: 200, exclusive: true})
	if err := r.Extend(170, bs("g1b"), 200); !errors.Is(err, ErrNotExtended) {
		t.Fatalf("equal end want ErrNotExtended, got %v", err)
	}
	if err := r.Extend(170, bs("g1b"), 190); !errors.Is(err, ErrNotExtended) {
		t.Fatalf("smaller end want ErrNotExtended, got %v", err)
	}
	var ce ConflictError
	if err := r.Extend(170, bs("g1b"), 260); !errors.As(err, &ce) || string(ce.Conflict().ID) != "g4" {
		t.Fatalf("extend to 260 must conflict with g4, got %v", err)
	}
	if got := r.Get(bs("g1b")); got.End != 200 {
		t.Fatalf("rejected extend must keep end=200, got %d", got.End)
	}
	// JP 上的授权延长：与 DE 的 g4 无公共叶，与 g2（排除 DE）在 JP 时间窗不相交于延长段
	r.mustAdd(t, addArgs{now: 170, id: "gh", title: "T", licensee: "乙", node: "JP", start: 100, end: 200, exclusive: false})
	if err := r.Extend(170, bs("gh"), 210); err != nil {
		t.Fatalf("unconflicted extend should pass, got %v", err)
	}
	if got := r.Get(bs("gh")); got.End != 210 {
		t.Fatalf("end = %d, want 210", got.End)
	}
	// Extend 参数与次序错误
	if err := r.Extend(170, bs("gh"), MaxTime+1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("got %v", err)
	}
	if err := r.Extend(160, bs("gh"), 999); !errors.Is(err, ErrClockMovedBack) {
		t.Fatalf("got %v", err)
	}
	if err := r.Extend(170, bs("nope"), 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}
