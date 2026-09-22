package query

import (
	"reflect"
	"testing"
)

func mustParse(t *testing.T, raw string) []Item {
	t.Helper()
	items, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse(%q): %v", raw, err)
	}
	return items
}

func TestThreeStatesDistinguishable(t *testing.T) {
	a := mustParse(t, "a")
	b := mustParse(t, "a=")
	c := mustParse(t, "a=%20")
	if a[0].HasValue || !b[0].HasValue || b[0].Value != "" {
		t.Fatalf("state parse wrong: %+v %+v", a[0], b[0])
	}
	if c[0].Value != "%20" {
		t.Fatalf("space must stay encoded, got %q", c[0].Value)
	}
	forms := map[string]bool{Render(a): true, Render(b): true, Render(c): true}
	if len(forms) != 3 {
		t.Errorf("three states collapsed: %v", forms)
	}
}

func TestRepeatedKeysKept(t *testing.T) {
	items := mustParse(t, "a=1&a=2")
	if len(items) != 2 {
		t.Fatalf("repeated keys dropped: %v", items)
	}
	if Render(items) != "a=1&a=2" {
		t.Errorf("ordered render = %q", Render(items))
	}
	s1 := Render(Sorted(mustParse(t, "a=1&a=2")))
	s2 := Render(Sorted(mustParse(t, "a=2&a=1")))
	if s1 != s2 || s1 != "a=1&a=2" {
		t.Errorf("sorted forms differ: %q vs %q", s1, s2)
	}
}

func TestSortByKeyThenValue(t *testing.T) {
	items := mustParse(t, "b=2&a=2&a=1&b=1")
	got := Render(Sorted(items))
	if got != "a=1&a=2&b=1&b=2" {
		t.Errorf("sorted = %q", got)
	}
}

func TestReservedEscapeKept(t *testing.T) {
	items := mustParse(t, "k=%3D%26")
	if items[0].Value != "%3D%26" {
		t.Errorf("reserved escapes must stay: %q", items[0].Value)
	}
	items = mustParse(t, "%6B=%76") // unreserved k, v fold to literals
	if items[0].Key != "k" || items[0].Value != "v" {
		t.Errorf("unreserved escapes should fold: %+v", items[0])
	}
}

func TestEmptyPartsAndIdempotency(t *testing.T) {
	for _, raw := range []string{"", "a=1&&b=2", "a&a=&a=%20", "x=%2f&y=%41"} {
		once := Render(mustParse(t, raw))
		twice := Render(mustParse(t, once))
		if once != twice {
			t.Errorf("not idempotent: %q -> %q -> %q", raw, once, twice)
		}
	}
	items := mustParse(t, "a=1&&b=2")
	want := []Item{{Key: "a", Value: "1", HasValue: true}, {}, {Key: "b", Value: "2", HasValue: true}}
	if !reflect.DeepEqual(items, want) {
		t.Errorf("empty part not preserved: %+v", items)
	}
}

func TestBadEscapePropagates(t *testing.T) {
	if _, err := Parse("a=%zz"); err == nil {
		t.Error("expected pct error")
	}
}
