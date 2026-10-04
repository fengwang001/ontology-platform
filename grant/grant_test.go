package grant

import (
	"errors"
	"fmt"
	"testing"

	"ontology/territory"
)

func newExampleRegistry(t *testing.T) *Registry {
	t.Helper()
	tr, err := territory.New(map[string][]string{
		"WORLD": {"EU", "AS"},
		"EU":    {"FR", "DE"},
		"AS":    {"JP", "KR"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return NewRegistry(tr)
}

func TestSpecExampleConflictSequence(t *testing.T) {
	r := newExampleRegistry(t)
	mustAdd := func(id, who, node string, excl []string, s, e int64, exclusive bool) {
		t.Helper()
		if err := r.Add(0, id, "T", who, node, excl, s, e, exclusive); err != nil {
			t.Fatalf("Add %s: %v", id, err)
		}
	}
	mustAdd("g1", "甲", "EU", []string{"FR"}, 100, 200, true)
	mustAdd("g2", "乙", "WORLD", []string{"DE"}, 150, 300, true)
	mustAdd("g4", "丙", "DE", nil, 200, 250, false)

	var ce *ConflictError
	err := r.Add(0, "g5", "T", "丙", "EU", nil, 199, 210, false)
	if !errors.As(err, &ce) || ce.ID != "g1" {
		t.Fatalf("g5 conflict: err=%v want g1", err)
	}

	if err := r.Revoke(160, "g1"); err != nil {
		t.Fatalf("revoke g1: %v", err)
	}
	err = r.Add(161, "g5", "T", "丙", "EU", nil, 199, 210, false)
	if !errors.As(err, &ce) || ce.ID != "g2" {
		t.Fatalf("g5 after revoke: err=%v want g2", err)
	}
}

func TestSameLicenseeOverlap(t *testing.T) {
	r := newExampleRegistry(t)
	if err := r.Add(0, "a", "T", "甲", "EU", nil, 0, 100, true); err != nil {
		t.Fatal(err)
	}
	if err := r.Add(1, "b", "T", "甲", "WORLD", nil, 50, 150, true); err != nil {
		t.Fatalf("same licensee overlap: %v", err)
	}
	// 甲的独占覆盖 WORLD[50,150) 与 [0,100) 合并后覆盖全部叶全部时间，
	// 无法在该 title 再放他方授权；另起登记簿验证非独占不对称性。
	r2 := newExampleRegistry(t)
	if err := r2.Add(0, "c", "T", "乙", "JP", nil, 0, 60, false); err != nil {
		t.Fatal(err)
	}
	if err := r2.Add(0, "d", "T", "丙", "JP", nil, 0, 60, false); err != nil {
		t.Fatalf("non-excl vs non-excl: %v", err)
	}
	// 独占进入非独占区域冲突（非独占 vs 独占的不对称）。
	if err := r2.Add(0, "e", "T", "丁", "JP", nil, 0, 60, true); err == nil {
		t.Fatalf("exclusive vs non-exclusive should conflict")
	}
}

func TestEndpointTouchNoConflict(t *testing.T) {
	r := newExampleRegistry(t)
	if err := r.Add(0, "a", "T", "甲", "DE", nil, 100, 200, true); err != nil {
		t.Fatal(err)
	}
	if err := r.Add(0, "b", "T", "乙", "DE", nil, 200, 300, true); err != nil {
		t.Fatalf("endpoint touch: %v", err)
	}
}

func TestAncestorButNoSharedLeaf(t *testing.T) {
	r := newExampleRegistry(t)
	if err := r.Add(0, "a", "T", "甲", "EU", []string{"FR"}, 0, 100, true); err != nil {
		t.Fatal(err)
	}
	if err := r.Add(0, "b", "T", "乙", "EU", []string{"DE"}, 0, 100, true); err != nil {
		t.Fatalf("no shared leaf despite ancestor relation: %v", err)
	}
}

func TestConflictSmallestID(t *testing.T) {
	r := newExampleRegistry(t)
	for _, id := range []string{"z9", "a1", "m5"} {
		if err := r.Add(0, id, "T", "乙", "DE", nil, 0, 100, true); err != nil {
			t.Fatal(err)
		}
	}
	var ce *ConflictError
	err := r.Add(1, "new", "T", "甲", "DE", nil, 0, 100, false)
	if !errors.As(err, &ce) || ce.ID != "a1" {
		t.Fatalf("err=%v want conflict a1", err)
	}
}

func TestAddRejectionOrder(t *testing.T) {
	r := newExampleRegistry(t)
	if err := r.Add(50, "dup", "T", "甲", "DE", nil, 10, 20, false); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		now     int64
		id      string
		node    string
		excl    []string
		s, e    int64
		wantErr error
	}{
		{"bad range", 60, "x1", "DE", nil, 200, 100, ErrInvalidArg},
		{"negative start", 60, "x1", "DE", nil, -1, 100, ErrInvalidArg},
		{"too many excludes", 60, "x1", "WORLD",
			[]string{"EU", "AS", "FR", "DE", "JP", "KR", "EU", "FR", "DE"}, 0, 100, ErrInvalidArg},
		{"dup exclude", 60, "x1", "WORLD", []string{"FR", "FR"}, 0, 100, ErrInvalidArg},
		{"clock back", 40, "x1", "DE", nil, 10, 20, ErrClockBack},
		{"duplicate id", 60, "dup", "DE", nil, 10, 20, ErrDuplicateID},
		{"unknown node", 60, "x1", "MARS", nil, 10, 20, ErrUnknownRegion},
		{"unknown exclude", 60, "x1", "DE", []string{"MARS"}, 10, 20, ErrUnknownRegion},
		{"exclude not descendant", 60, "x1", "FR", []string{"DE"}, 10, 20, ErrBadExcludes},
		{"exclude self", 60, "x1", "DE", []string{"DE"}, 10, 20, ErrBadExcludes},
		{"excludes ancestor-related", 60, "x1", "WORLD", []string{"EU", "FR"}, 10, 20, ErrBadExcludes},
		{"empty coverage", 60, "x1", "EU", []string{"FR", "DE"}, 10, 20, ErrEmptyCoverage},
	}
	for _, c := range cases {
		err := r.Add(c.now, c.id, "T", "丙", c.node, c.excl, c.s, c.e, true)
		if !errors.Is(err, c.wantErr) {
			t.Errorf("%s: err=%v want %v", c.name, err, c.wantErr)
		}
	}
}

func TestRevokeBranches(t *testing.T) {
	r := newExampleRegistry(t)
	if err := r.Add(100, "g", "T", "甲", "DE", nil, 100, 200, true); err != nil {
		t.Fatal(err)
	}
	if err := r.Revoke(100, "g"); err != nil {
		t.Fatalf("revoke at start: %v", err)
	}
	if _, ok := r.byID["g"]; ok {
		t.Fatalf("g should be deleted when now==start")
	}

	if err := r.Add(100, "h", "T", "甲", "DE", nil, 100, 200, true); err != nil {
		t.Fatal(err)
	}
	if err := r.Revoke(200, "h"); !errors.Is(err, ErrExpired) {
		t.Fatalf("revoke at end: %v want ErrExpired", err)
	}
	if err := r.Revoke(150, "h"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if g := r.byID["h"]; g.End != 150 {
		t.Fatalf("End=%d want 150", g.End)
	}
	if err := r.Revoke(149, "h"); !errors.Is(err, ErrClockBack) {
		t.Fatalf("clock back: %v", err)
	}
	if err := r.Revoke(160, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("not found: %v", err)
	}
	if err := r.Revoke(-1, "h"); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("invalid: %v", err)
	}
}

func TestExtend(t *testing.T) {
	r := newExampleRegistry(t)
	if err := r.Add(100, "g1", "T", "甲", "DE", nil, 100, 150, true); err != nil {
		t.Fatal(err)
	}
	if err := r.Add(100, "g4", "T", "丙", "DE", nil, 200, 250, false); err != nil {
		t.Fatal(err)
	}
	// 新窗 [100,260) 与 g4 [200,250) 在 DE 冲突；原 end 保持 150。
	var ce *ConflictError
	err := r.Extend(120, "g1", 260)
	if !errors.As(err, &ce) {
		t.Fatalf("extend conflict: %v", err)
	}
	if g := r.byID["g1"]; g.End != 150 {
		t.Fatalf("g1 End changed after failed extend: %d", g.End)
	}
	if err := r.Extend(120, "g1", 150); !errors.Is(err, ErrNotExtended) {
		t.Fatalf("equal end: %v", err)
	}
	if err := r.Extend(120, "g1", 140); !errors.Is(err, ErrNotExtended) {
		t.Fatalf("shorter end: %v", err)
	}
	// 延到 180（<g4 起点 200）合法；之后再延到 260 仍冲突且不改原 end。
	if err := r.Extend(120, "g1", 180); err != nil {
		t.Fatalf("extend to 180: %v", err)
	}
	if err := r.Extend(121, "g1", 260); !errors.Is(err, ErrConflict) {
		t.Fatalf("re-extend conflict: %v", err)
	}
	if g := r.byID["g1"]; g.End != 180 {
		t.Fatalf("g1 End=%d want 180 after failed re-extend", g.End)
	}
	// 已到期：end<=now。
	r2 := newExampleRegistry(t)
	if err := r2.Add(100, "x", "T", "甲", "DE", nil, 100, 160, false); err != nil {
		t.Fatal(err)
	}
	if err := r2.Extend(170, "x", 200); !errors.Is(err, ErrExpired) {
		t.Fatalf("extend expired: %v", err)
	}
	if err := r2.Extend(170, "missing", 200); !errors.Is(err, ErrNotFound) {
		t.Fatalf("extend missing: %v", err)
	}
	if err := r.Extend(130, "g1", 100); !errors.Is(err, ErrNotExtended) {
		t.Fatalf("extend order: %v", err)
	}
}

func leafName(i int) string { return fmt.Sprintf("L%06d", i) }

func TestCounters(t *testing.T) {
	measureSpans := func(leaves int) int {
		kids := map[string][]string{"WORLD": {}}
		for i := 0; i < leaves; i++ {
			kids["WORLD"] = append(kids["WORLD"], leafName(i))
		}
		tr, err := territory.New(kids)
		if err != nil {
			t.Fatal(err)
		}
		r := NewRegistry(tr)
		ex1, ex2 := []string{}, []string{}
		for i := 0; i < 8; i++ {
			ex1 = append(ex1, leafName(i))
			ex2 = append(ex2, leafName(i+8))
		}
		if err := r.Add(0, "a", "T", "甲", "WORLD", ex1, 0, 100, true); err != nil {
			t.Fatal(err)
		}
		_ = r.Add(0, "b", "T", "乙", "WORLD", ex2, 0, 100, true)
		return r.spans
	}
	s100 := measureSpans(100)
	s10000 := measureSpans(10000)
	if s100 != s10000 {
		t.Fatalf("spans depends on leaf count: %d vs %d", s100, s10000)
	}
	if s100 > 18 {
		t.Fatalf("spans=%d > ex1+ex2+2=18", s100)
	}

	measureCompared := func(otherTitles int) int {
		tr, err := territory.New(map[string][]string{"WORLD": {"A", "B"}})
		if err != nil {
			t.Fatal(err)
		}
		r := NewRegistry(tr)
		for i := 0; i < otherTitles; i++ {
			id := fmt.Sprintf("o%d", i)
			if err := r.Add(0, id, id, "甲", "A", nil, 0, 100, true); err != nil {
				t.Fatal(err)
			}
		}
		// 同 title 下 3 条授权。
		if err := r.Add(0, "t1", "T", "甲", "A", nil, 0, 100, true); err != nil {
			t.Fatal(err)
		}
		if err := r.Add(0, "t2", "T", "甲", "A", nil, 0, 100, true); err != nil {
			t.Fatal(err)
		}
		if err := r.Add(0, "t3", "T", "甲", "A", nil, 0, 100, true); err != nil {
			t.Fatal(err)
		}
		_ = r.Add(0, "new", "T", "乙", "A", nil, 0, 100, true)
		return r.compared
	}
	c100 := measureCompared(100)
	c10000 := measureCompared(10000)
	if c100 != 3 || c10000 != 3 {
		t.Fatalf("compared = %d,%d want 3,3", c100, c10000)
	}
}
