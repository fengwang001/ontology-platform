package bplus

import "testing"

func TestEmptyRoot(t *testing.T) {
	loader, err := NewBulkLoader(4, 4, 50)
	if err != nil {
		t.Fatal(err)
	}
	if err := loader.Finish(); err != nil {
		t.Fatal(err)
	}

	pages := loader.Pages()
	t.Logf("input=[] output=%v basis=empty input must produce one empty leaf root", pages)
	if len(pages) != 1 || len(pages[0]) != 1 || len(pages[0][0].Keys) != 0 {
		t.Fatalf("unexpected empty root: %#v", pages)
	}
}
