package rtree

import "testing"

func TestSpecExample(t *testing.T) {
	tree, err := New(4, 2, 100)
	if err != nil {
		t.Fatal(err)
	}

	rects := []Rect{
		{0, 0, 2, 2},
		{4, 0, 6, 2},
		{0, 4, 2, 6},
		{4, 4, 6, 6},
		{10, 0, 12, 2},
		{10, 4, 12, 6},
		{1, 1, 5, 5},
	}
	for index, rect := range rects {
		if _, err := tree.Insert(int64(index+1), rect); err != nil {
			t.Fatalf("insert %d: %v", index+1, err)
		}
	}

	want := "N[0 0 12 6]{L[0 0 6 6](1 3 2 7),L[4 0 12 6](4 5 6)}"
	if got := tree.Dump(); got != want {
		t.Fatalf("dump = %q, want %q", got, want)
	}

	ids, err := tree.Search(Rect{2, 2, 4, 4})
	if err != nil {
		t.Fatal(err)
	}
	assertIDs(t, ids, []int64{1, 2, 3, 4, 7})

	if _, _, err := tree.Delete(1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := tree.Delete(3); err != nil {
		t.Fatal(err)
	}
	removed, reinserted, err := tree.Delete(2)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 || reinserted != 1 {
		t.Fatalf("delete(2) = %d,%d, want 1,1", removed, reinserted)
	}

	want = "L[1 0 12 6](4 5 6 7)"
	if got := tree.Dump(); got != want {
		t.Fatalf("dump = %q, want %q", got, want)
	}
}

func assertIDs(t *testing.T, got, want []int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	for index := range got {
		if got[index] != want[index] {
			t.Fatalf("ids = %v, want %v", got, want)
		}
	}
}
