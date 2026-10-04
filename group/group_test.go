package group

import "testing"

func TestTablePlaceholderAndPending(t *testing.T) {
	tab := NewTable()
	if got := tab.Lookup("missing"); got.Placeholder != 0 || got.Pending != 0 {
		t.Fatalf("missing group should be zero state, got %+v", got)
	}
	if tab.Groups() != 0 {
		t.Fatalf("Lookup must not register groups: %d", tab.Groups())
	}

	g := tab.Ensure("g")
	g.Placeholder = 1
	g.Pending = 2
	if got := tab.Lookup("g"); got.Placeholder != 1 || got.Pending != 2 {
		t.Fatalf("Lookup g = %+v", got)
	}

	// ClearIfEmpty keeps a non-empty group untouched.
	tab.ClearIfEmpty("g")
	if tab.Groups() != 1 {
		t.Fatalf("non-empty group must remain: %d", tab.Groups())
	}

	g.Pending = 0
	g.Placeholder = 0
	tab.ClearIfEmpty("g")
	if tab.Groups() != 0 {
		t.Fatalf("emptied group should be removed: %d", tab.Groups())
	}
	if got := tab.Lookup("g"); got.Placeholder != 0 || got.Pending != 0 {
		t.Fatalf("removed group reads zero: %+v", got)
	}
}

func TestEnsureIdempotent(t *testing.T) {
	tab := NewTable()
	a := tab.Ensure("a")
	a.Placeholder = 7
	b := tab.Ensure("a")
	if b.Placeholder != 7 {
		t.Fatalf("Ensure returned a different state: %+v", b)
	}
}
