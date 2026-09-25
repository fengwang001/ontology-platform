package ontology

import (
	"errors"
	"testing"
)

func TestFindUnknownElementReturnsError(t *testing.T) {
	var ds DisjointSet
	if _, err := ds.Find("ghost"); !errors.Is(err, ErrUnknownElement) {
		t.Fatalf("Find on unknown ID: got %v, want ErrUnknownElement", err)
	}
	// Find must not implicitly create the element.
	if got := ds.ClassCount(); got != 0 {
		t.Fatalf("ClassCount after failed Find = %d, want 0", got)
	}
	if _, err := ds.Find("ghost"); !errors.Is(err, ErrUnknownElement) {
		t.Fatalf("Find still unknown after failed Find: got %v", err)
	}
}

func TestUnionImplicitlyCreatesElements(t *testing.T) {
	var ds DisjointSet
	ds.Union("a", "b")
	if got := ds.ClassCount(); got != 1 {
		t.Fatalf("ClassCount = %d, want 1", got)
	}
	for _, id := range []string{"a", "b"} {
		rep, err := ds.Find(id)
		if err != nil {
			t.Fatalf("Find(%q) after Union: unexpected error %v", id, err)
		}
		if rep != "a" {
			t.Fatalf("Find(%q) = %q, want %q", id, rep, "a")
		}
	}
}

func TestAddIsIdempotent(t *testing.T) {
	var ds DisjointSet
	ds.Add("x")
	ds.Add("x")
	ds.Add("x")
	if got := ds.ClassCount(); got != 1 {
		t.Fatalf("ClassCount after repeated Add = %d, want 1", got)
	}
	// Add on an element that already belongs to a bigger class is a no-op.
	ds.Union("x", "y")
	ds.Add("x")
	if got := ds.ClassCount(); got != 1 {
		t.Fatalf("ClassCount after Add on merged element = %d, want 1", got)
	}
}

func TestEmptyStringIsLegalID(t *testing.T) {
	var ds DisjointSet
	ds.Add("")
	rep, err := ds.Find("")
	if err != nil {
		t.Fatalf("Find(\"\"): unexpected error %v", err)
	}
	if rep != "" {
		t.Fatalf("Find(\"\") = %q, want empty string", rep)
	}
	ds.Union("", "a")
	// Empty string sorts before everything, so it becomes the representative.
	if rep, _ := ds.Find("a"); rep != "" {
		t.Fatalf("Find(\"a\") = %q, want empty string", rep)
	}
	if got := ds.ClassCount(); got != 1 {
		t.Fatalf("ClassCount = %d, want 1", got)
	}
}

func TestConnectedDistinguishesUnknownFromFalse(t *testing.T) {
	var ds DisjointSet
	ds.Add("a")
	ds.Add("b")

	ok, err := ds.Connected("a", "b")
	if err != nil {
		t.Fatalf("Connected(known, known): unexpected error %v", err)
	}
	if ok {
		t.Fatal("Connected(a, b) = true before any Union, want false")
	}

	if _, err := ds.Connected("a", "ghost"); !errors.Is(err, ErrUnknownElement) {
		t.Fatalf("Connected(known, unknown): got %v, want ErrUnknownElement", err)
	}
	if _, err := ds.Connected("ghost", "a"); !errors.Is(err, ErrUnknownElement) {
		t.Fatalf("Connected(unknown, known): got %v, want ErrUnknownElement", err)
	}
	if _, err := ds.Connected("g1", "g2"); !errors.Is(err, ErrUnknownElement) {
		t.Fatalf("Connected(unknown, unknown): got %v, want ErrUnknownElement", err)
	}

	ds.Union("a", "b")
	ok, err = ds.Connected("a", "b")
	if err != nil || !ok {
		t.Fatalf("Connected after Union = %v, %v; want true, nil", ok, err)
	}
}

func TestClassCountTracksMerges(t *testing.T) {
	var ds DisjointSet
	for _, id := range []string{"a", "b", "c", "d"} {
		ds.Add(id)
	}
	if got := ds.ClassCount(); got != 4 {
		t.Fatalf("ClassCount = %d, want 4", got)
	}
	ds.Union("a", "b")
	ds.Union("c", "d")
	if got := ds.ClassCount(); got != 2 {
		t.Fatalf("ClassCount = %d, want 2", got)
	}
	ds.Union("b", "c")
	if got := ds.ClassCount(); got != 1 {
		t.Fatalf("ClassCount = %d, want 1", got)
	}
}

func TestRepeatedUnionKeepsCountAndRepresentatives(t *testing.T) {
	var ds DisjointSet
	ds.Union("alpha", "beta")
	ds.Union("gamma", "delta")
	ds.Union("beta", "gamma")
	wantCount := ds.ClassCount()
	if wantCount != 1 {
		t.Fatalf("setup: ClassCount = %d, want 1", wantCount)
	}
	wantReps := map[string]string{}
	for _, id := range []string{"alpha", "beta", "gamma", "delta"} {
		rep, err := ds.Find(id)
		if err != nil {
			t.Fatalf("setup: Find(%q): %v", id, err)
		}
		wantReps[id] = rep
	}

	const repeats = 1_000_000
	for i := 0; i < repeats; i++ {
		ds.Union("alpha", "delta")
	}

	if got := ds.ClassCount(); got != wantCount {
		t.Fatalf("ClassCount after %d repeated Unions = %d, want %d", repeats, got, wantCount)
	}
	for id, want := range wantReps {
		rep, err := ds.Find(id)
		if err != nil {
			t.Fatalf("Find(%q) after repeated Unions: %v", id, err)
		}
		if rep != want {
			t.Fatalf("Find(%q) = %q after repeated Unions, want %q", id, rep, want)
		}
	}
}
