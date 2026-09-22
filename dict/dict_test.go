package dict

import (
	"errors"
	"testing"
)

func TestRoundTripInt(t *testing.T) {
	vals := []int64{4, -9, 4, 1<<62, 0, -9, 1<<62, -1 << 62}
	d, err := Build(vals, 0)
	if err != nil {
		t.Fatal(err)
	}
	if d.Len() != 5 {
		t.Fatalf("card = %d, want 5", d.Len())
	}
	// First-seen order.
	if d.Value(0) != 4 || d.Value(1) != -9 {
		t.Fatal("unexpected first-seen order")
	}
	codes := Encode(d, vals)
	back := Decode(d, codes)
	for i := range vals {
		if back[i] != vals[i] {
			t.Fatalf("i=%d got %d want %d", i, back[i], vals[i])
		}
	}
	if c, ok := d.Code(123); ok {
		t.Fatalf("absent value reported code %d", c)
	}
}

func TestRoundTripString(t *testing.T) {
	vals := []string{"", "a", "", "b", "a", "hello"}
	d, err := Build(vals, 0)
	if err != nil {
		t.Fatal(err)
	}
	if d.Len() != 4 {
	t.Fatalf("card = %d want 4", d.Len())
	}
	back := Decode(d, Encode(d, vals))
	for i := range vals {
		if back[i] != vals[i] {
			t.Fatalf("i=%d got %q want %q", i, back[i], vals[i])
		}
	}
	// Empty string is a real member distinct from absence.
	if _, ok := d.Code(""); !ok {
		t.Fatal("empty string missing from dictionary")
	}
}

func TestCardinalityLimit(t *testing.T) {
	d, err := Build([]int64{1, 2, 3, 4}, 3)
	if !errors.Is(err, ErrTooMany) || d != nil {
		t.Fatalf("got d=%v err=%v, want ErrTooMany", d, err)
	}
	d2, err := Build([]int64{1, 1, 2}, 2)
	if err != nil || d2.Len() != 2 {
		t.Fatalf("within-limit build failed: %v", err)
	}
}
