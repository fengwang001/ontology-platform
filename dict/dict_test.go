package dict

import "testing"

func TestRoundTrip(t *testing.T) {
	ints := []int64{5, -1, 5, 1<<63 - 1, -1 << 63, 0, -1, 5}
	di := NewInt(ints)
	if di.Cardinality() != 5 {
		t.Fatalf("int card=%d want 5", di.Cardinality())
	}
	codes := di.Encode(ints)
	back, err := di.Decode(codes)
	if err != nil {
		t.Fatal(err)
	}
	for i := range ints {
		if back[i] != ints[i] {
			t.Fatalf("int idx=%d got=%d want=%d", i, back[i], ints[i])
		}
	}
	// First-seen codes are dense and map back correctly.
	for c, want := range di.Values() {
		got, err := di.Lookup(uint64(c))
		if err != nil || got != want {
			t.Fatalf("lookup %d: got %d want %d err %v", c, got, want, err)
		}
		if di.Code(want) != uint64(c) {
			t.Fatalf("reverse code mismatch for %d", want)
		}
	}
	if _, err := di.Lookup(uint64(di.Cardinality())); err != ErrCode {
		t.Fatalf("expected ErrCode, got %v", err)
	}

	// Empty string must be a first-class member, distinct from any other.
	strs := []string{"", "a", "", "b", "a", ""}
	ds := NewStr(strs)
	if ds.Cardinality() != 3 {
		t.Fatalf("str card=%d want 3", ds.Cardinality())
	}
	sc := ds.Encode(strs)
	sback, err := ds.Decode(sc)
	if err != nil {
		t.Fatal(err)
	}
	for i := range strs {
		if sback[i] != strs[i] {
			t.Fatalf("str idx=%d got=%q want=%q", i, sback[i], strs[i])
		}
	}
	if ds.Code("") != 0 { // empty is a real code, not "missing"
		t.Fatal("empty string code should be 0")
	}

	if NewInt(nil).Cardinality() != 0 {
		t.Fatal("empty dict cardinality")
	}
}
