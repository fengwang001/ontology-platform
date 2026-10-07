package ontology

import (
	"fmt"
	"testing"
)

// testSchema builds the fixture used by every test: one object type Order with
// region and category group attributes and a numeric amount; three views
// (sum by region, sum by category, count by region) so one commit touches
// multiple views at once.
func testSchema() ([]ObjectType, []ViewSpec) {
	types := []ObjectType{{
		Name: "Order",
		Attrs: map[string]AttrSpec{
			"region":   {Name: "region", Type: AttrString, Required: true},
			"category": {Name: "category", Type: AttrString, Required: true},
			"amount":   {Name: "amount", Type: AttrInt, Required: true},
		},
	}}
	views := []ViewSpec{
		{Name: "AmountByRegion", Kind: AggSum, Sources: []SourceSpec{{
			Type: "Order", GroupAttr: "region", ValueAttr: "amount", GroupValid: true,
		}}},
		{Name: "AmountByCategory", Kind: AggSum, Sources: []SourceSpec{{
			Type: "Order", GroupAttr: "category", ValueAttr: "amount", GroupValid: true,
		}}},
		{Name: "CountByRegion", Kind: AggCount, Sources: []SourceSpec{{
			Type: "Order", GroupAttr: "region", GroupValid: true,
		}}},
	}
	return types, views
}

func order(region, category string, amount int) map[string]any {
	return map[string]any{"region": region, "category": category, "amount": amount}
}

func mustCode(t *testing.T, err error, want ErrorCode) *OpError {
	t.Helper()
	if err == nil {
		t.Fatalf("want error %s, got success", want)
	}
	oe, ok := err.(*OpError)
	if !ok {
		t.Fatalf("want *OpError, got %T: %v", err, err)
	}
	if oe.Code != want {
		t.Fatalf("want code %s, got %s (%v)", want, oe.Code, oe)
	}
	return oe
}

func assertGroup(t *testing.T, s *Store, view, group string, wantVal float64, wantCount int64) {
	t.Helper()
	got := s.Query(view, group)
	if !got.Exists || got.Value != wantVal || got.Count != wantCount {
		t.Fatalf("query %s[%s] = (exists=%v,value=%v,count=%d); want (true,%v,%d)",
			view, group, got.Exists, got.Value, got.Count, wantVal, wantCount)
	}
}

func assertEmpty(t *testing.T, s *Store, view, group string) {
	t.Helper()
	got := s.Query(view, group)
	if got.Exists || got.Value != 0 || got.Count != 0 {
		t.Fatalf("query %s[%s] = %+v; want absent group", view, group, got)
	}
}

func logLine(t *testing.T, format string, args ...any) {
	t.Helper()
	t.Logf("  | %s", fmt.Sprintf(format, args...))
}
