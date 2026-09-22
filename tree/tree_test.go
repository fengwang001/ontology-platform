package tree

import (
	"errors"
	"math/rand"
	"testing"

	"ontology/ival"
)

func iv(lo, hi int64) ival.Interval { return ival.Interval{Lo: lo, Hi: hi} }

func TestInsertDeleteErrors(t *testing.T) {
	cases := []struct {
		name string
		op   func(t *Tree) error
		want error
	}{
		{"insert-invalid", func(t *Tree) error { return t.Insert(iv(5, 1)) }, ival.ErrInvalid},
		{"delete-missing", func(t *Tree) error { return t.Delete(iv(7, 8)) }, ErrNotFound},
		{"delete-invalid", func(t *Tree) error { return t.Delete(iv(9, 3)) }, ival.ErrInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr := New()
			if err := tr.Insert(iv(1, 2)); err != nil {
				t.Fatal(err)
			}
			before := tr.Size()
			if err := c.op(tr); !errors.Is(err, c.want) {
				t.Fatalf("got %v want %v", err, c.want)
			}
			if tr.Size() != before {
				t.Fatal("failed op must not change state")
			}
			if err := tr.Check(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMaxIntervals(t *testing.T) {
	tr := New(WithMaxIntervals(2))
	for _, x := range []ival.Interval{iv(0, 1), iv(1, 2)} {
		if err := tr.Insert(x); err != nil {
			t.Fatal(err)
		}
	}
	if err := tr.Insert(iv(2, 3)); !errors.Is(err, ErrTooMany) {
		t.Fatalf("got %v want ErrTooMany", err)
	}
	if tr.Size() != 2 {
		t.Fatal("rejected insert must not leave a node behind")
	}
	if err := tr.Delete(iv(1, 2)); err != nil {
		t.Fatal(err)
	}
	if err := tr.Insert(iv(2, 3)); err != nil {
		t.Fatalf("insert after delete must succeed: %v", err)
	}
	if err := tr.Check(); err != nil {
		t.Fatal(err)
	}
}

func TestZeroLengthInsertDelete(t *testing.T) {
	tr := New()
	if err := tr.Insert(iv(2, 2)); err != nil {
		t.Fatalf("zero-length interval must be insertable: %v", err)
	}
	if err := tr.Delete(iv(2, 2)); err != nil {
		t.Fatal(err)
	}
	if tr.Size() != 0 {
		t.Fatal("size must return to 0")
	}
}

func TestRandomModel(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	tr := New()
	model := map[ival.Interval]int{}
	for step := 0; step < 5000; step++ {
		x := ival.Interval{Lo: rng.Int63n(100), Hi: rng.Int63n(100)}
		if x.Lo > x.Hi {
			x.Lo, x.Hi = x.Hi, x.Lo
		}
		x.Payload = string(rune('a' + rng.Intn(3)))
		if rng.Intn(2) == 0 || len(model) == 0 {
			if err := tr.Insert(x); err != nil {
				t.Fatal(err)
			}
			model[x]++
		} else {
			keys := make([]ival.Interval, 0, len(model))
			for k := range model {
				keys = append(keys, k)
			}
			k := keys[rng.Intn(len(keys))]
			if err := tr.Delete(k); err != nil {
				t.Fatalf("delete of present interval failed: %v", err)
			}
			if model[k]--; model[k] == 0 {
				delete(model, k)
			}
		}
		total := 0
		for _, c := range model {
			total += c
		}
		if tr.Size() != total {
			t.Fatalf("step %d: size %d != model %d", step, tr.Size(), total)
		}
		if err := tr.Check(); err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
	}
}

func TestMultisetDeleteOne(t *testing.T) {
	tr := New()
	x := iv(1, 5)
	for i := 0; i < 3; i++ {
		if err := tr.Insert(x); err != nil {
			t.Fatal(err)
		}
	}
	if err := tr.Delete(x); err != nil {
		t.Fatal(err)
	}
	if tr.Size() != 2 {
		t.Fatalf("size %d want 2", tr.Size())
	}
	if err := tr.Check(); err != nil {
		t.Fatal(err)
	}
}
