package dict

import "testing"

func TestRoundTrip(t *testing.T) {
	d := New(0)
	vals := []int64{7, 7, -3, 42, 7, -3, 0}
	codes, err := d.Encode(vals)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if d.Size() != 4 {
		t.Fatalf("size %d, want 4", d.Size())
	}
	got := d.Decode(codes)
	for i, v := range vals {
		if got[i] != v {
			t.Fatalf("value %d: got %d, want %d", i, got[i], v)
		}
	}
}

func TestCardinalityLimit(t *testing.T) {
	d := New(2)
	if _, err := d.Encode([]int64{1, 2}); err != nil {
		t.Fatalf("encode within limit: %v", err)
	}
	d2 := New(2)
	if _, err := d2.Encode([]int64{1, 2, 3}); err != ErrCardinality {
		t.Fatalf("want ErrCardinality, got %v", err)
	}
	if d2.Size() != 0 {
		t.Fatalf("rejected encode mutated dictionary: size %d", d2.Size())
	}
	// Same new value repeated within one call counts once.
	d3 := New(2)
	if _, err := d3.Encode([]int64{9, 9, 9, 8}); err != nil {
		t.Fatalf("repeated new value: %v", err)
	}
}

func TestBuild(t *testing.T) {
	d := Build([]int64{10, 20, 30})
	if d.Value(2) != 30 || d.Size() != 3 {
		t.Fatalf("build: size %d, value %d", d.Size(), d.Value(2))
	}
}
