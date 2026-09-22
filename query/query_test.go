package query

import "testing"

func build(t *testing.T, raw string) string {
	t.Helper()
	items, _, err := Parse(raw)
	if err != nil {
		t.Fatalf("%q: %v", raw, err)
	}
	return Build(items)
}

func TestThreeStatesDistinct(t *testing.T) {
	a := build(t, "a")
	b := build(t, "a=")
	c := build(t, "a=%20")
	if a != "a" || b != "a=" || c != "a=%20" {
		t.Fatalf("got %q %q %q", a, b, c)
	}
	if a == b || b == c || a == c {
		t.Fatal("three states collapsed")
	}
}

func TestDuplicateKeysKept(t *testing.T) {
	if got := build(t, "a=1&a=2"); got != "a=1&a=2" {
		t.Fatalf("got %q", got)
	}
	items, _, _ := Parse("a=1&a=2")
	Sort(items)
	if Build(items) != "a=1&a=2" {
		t.Fatalf("sorted: %q", Build(items))
	}
	items, _, _ = Parse("a=2&a=1")
	Sort(items)
	if Build(items) != "a=1&a=2" {
		t.Fatalf("sorted: %q", Build(items))
	}
}

func TestSortByKeyThenValue(t *testing.T) {
	items, _, _ := Parse("b=2&a=2&a=1&a")
	Sort(items)
	if got := Build(items); got != "a&a=1&a=2&b=2" {
		t.Fatalf("got %q", got)
	}
}

func TestEmptyItemsDropped(t *testing.T) {
	items, st, err := Parse("a=1&&b=2&")
	if err != nil {
		t.Fatal(err)
	}
	if !st.Dropped || Build(items) != "a=1&b=2" {
		t.Fatalf("got %q %+v", Build(items), st)
	}
}

func TestEscapeNormalization(t *testing.T) {
	if got := build(t, "k%41=%2f%20"); got != "kA=%2F%20" {
		t.Fatalf("got %q", got)
	}
	items, st, _ := Parse("k=%41")
	if !st.EscFired {
		t.Fatal("EscFired expected")
	}
	_ = items
}

func TestRoundTripIdempotent(t *testing.T) {
	for _, in := range []string{"a&a=&a=%20", "b=2&a=1", "x=%2F%41", "k=a=b"} {
		once := build(t, in)
		if twice := build(t, once); once != twice {
			t.Errorf("%q: %q vs %q", in, once, twice)
		}
	}
}

func TestInvalidEscapePropagates(t *testing.T) {
	if _, _, err := Parse("a=%GG"); err == nil {
		t.Fatal("expected error")
	}
}
