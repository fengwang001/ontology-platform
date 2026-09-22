package query

import "testing"

func renderQuery(t *testing.T, raw string, mode Mode) string {
	t.Helper()
	_, s, err := Parse(raw, mode, nil)
	if err != nil {
		t.Fatalf("Parse(%q): %v", raw, err)
	}
	return s
}

func TestThreeStates(t *testing.T) {
	a := renderQuery(t, "a", ModeOrdered)
	b := renderQuery(t, "a=", ModeOrdered)
	c := renderQuery(t, "a=%20", ModeOrdered)
	if a == b || b == c || a == c {
		t.Fatalf("three states collapsed: %q %q %q", a, b, c)
	}
	if a != "a" || b != "a=" || c != "a=%20" {
		t.Fatalf("unexpected %q %q %q", a, b, c)
	}
	items, _, err := Parse("a&a=&a=%20", ModeOrdered, nil)
	if err != nil || len(items) != 3 {
		t.Fatalf("items=%v err=%v", items, err)
	}
	if items[0].HasEq || !items[1].HasEq || items[1].Value != "" || items[2].Value != "%20" {
		t.Fatalf("state flags wrong: %+v", items)
	}
}

func TestModesAndRepeats(t *testing.T) {
	orderedEq := renderQuery(t, "a=1&a=2", ModeOrdered) == renderQuery(t, "a=1&a=2", ModeOrdered)
	orderedNeq := renderQuery(t, "a=1&a=2", ModeOrdered) != renderQuery(t, "a=2&a=1", ModeOrdered)
	sortedEq := renderQuery(t, "a=1&a=2", ModeSorted) == renderQuery(t, "a=2&a=1", ModeSorted)
	if !orderedEq || !orderedNeq || !sortedEq {
		t.Fatal("mode/repeat semantics wrong")
	}
	if got := renderQuery(t, "b=2&a=1&a=0", ModeSorted); got != "a=0&a=1&b=2" {
		t.Fatalf("sorted=%q", got)
	}
	if got := renderQuery(t, "a=2&a=1", ModeSorted); got != "a=1&a=2" {
		t.Fatalf("repeat sort=%q", got)
	}
	if got := renderQuery(t, "A=%42", ModeOrdered); got != "A=B" {
		t.Fatalf("redundant escape not decoded: %q", got)
	}
}

func TestIdempotent(t *testing.T) {
	for _, raw := range []string{"a=%2f&b=%41", "%E4%B8%AD=x", "x&y=", "a=1&&b=2&"} {
		first := renderQuery(t, raw, ModeOrdered)
		second := renderQuery(t, first, ModeOrdered)
		if first != second {
			t.Fatalf("not idempotent %q -> %q -> %q", raw, first, second)
		}
	}
}

func TestErrors(t *testing.T) {
	cases := []struct {
		in  string
		off int
		bad func(*Error) bool
	}{
		{"a=%2", 2, func(e *Error) bool { return e.Kind == "truncated" }},
		{"%zz=1", 1, func(e *Error) bool { return e.Kind == "badhex" }},
		{"a=b&c=%ff", 6, func(e *Error) bool { return e.Kind == "utf8" }},
	}
	for _, c := range cases {
		_, _, err := Parse(c.in, ModeOrdered, nil)
		e, ok := err.(*Error)
		if !ok || !c.bad(e) || e.Offset != c.off {
			t.Errorf("Parse(%q)=%v want off=%d", c.in, err, c.off)
		}
	}
}
