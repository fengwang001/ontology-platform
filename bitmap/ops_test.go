package bitmap

import (
	"bytes"
	"math/rand"
	"testing"
)

// randomPair builds a random Bitmap and its naive twin over
// [0, size) using random ranges and single-bit updates.
func randomPair(rng *rand.Rand, size uint32) (*Bitmap, *naive) {
	b := New()
	n := newNaive(int(size))
	for i := 0; i < 40; i++ {
		lo := rng.Uint32() % size
		hi := lo + rng.Uint32()%(size/8+1)
		if hi >= size {
			hi = size - 1
		}
		b.SetRange(lo, hi)
		n.setRange(lo, hi)
	}
	for i := 0; i < 200; i++ {
		x := rng.Uint32() % size
		if rng.Intn(2) == 0 {
			b.Clear(x)
			n.clear(x)
		} else {
			b.Set(x)
			n.set(x)
		}
	}
	return b, n
}

func TestOpsMatchNaive(t *testing.T) {
	const size = 1 << 16
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 30; iter++ {
		a, na := randomPair(rng, size)
		b, nb := randomPair(rng, size)
		if got := a.Bytes(); !bytes.Equal(got, na.toBitmap().Bytes()) {
			t.Fatalf("iter %d: input A diverges from naive", iter)
		}
		u, _ := a.Union(b)
		i, _ := a.Intersect(b)
		d, _ := a.Difference(b)
		for name, got := range map[string]*Bitmap{"union": u, "intersect": i, "difference": d} {
			if err := got.Verify(); err != nil {
				t.Fatalf("iter %d %s: %v", iter, name, err)
			}
		}
		checks := []struct {
			name string
			got  *Bitmap
			want *naive
		}{
			{"union", u, na.union(nb)},
			{"intersect", i, na.intersect(nb)},
			{"difference", d, na.difference(nb)},
		}
		for _, c := range checks {
			if got := c.got.Count(); got != c.want.count() {
				t.Fatalf("iter %d %s: Count = %d, naive = %d",
					iter, c.name, got, c.want.count())
			}
			if !bytes.Equal(c.got.Bytes(), c.want.toBitmap().Bytes()) {
				t.Fatalf("iter %d %s: encoding diverges from naive", iter, c.name)
			}
		}
	}
}

// TestMergeStepsSmall proves the merge cost is proportional to the
// number of runs, not bits: two 2-run sets covering 10M bits merge
// in a single-digit number of cursor advances.
func TestMergeStepsSmall(t *testing.T) {
	a := New()
	a.SetRange(1_000_000, 5_999_999) // 2 runs, 5M bits
	b := New()
	b.SetRange(3_000_000, 7_999_999) // 2 runs, 5M bits
	if got := a.Count(); got != 5_000_000 {
		t.Fatalf("a.Count = %d", got)
	}
	if got := b.Count(); got != 5_000_000 {
		t.Fatalf("b.Count = %d", got)
	}
	inter, steps := a.Intersect(b)
	if steps >= 10 {
		t.Fatalf("intersect advanced %d runs, want single digit", steps)
	}
	if got := inter.Count(); got != 3_000_000 {
		t.Fatalf("intersect Count = %d, want 3M", got)
	}
	if got, _ := inter.Min(); got != 3_000_000 {
		t.Fatalf("intersect Min = %d", got)
	}
	if got, _ := inter.Max(); got != 5_999_999 {
		t.Fatalf("intersect Max = %d", got)
	}
	u, uSteps := a.Union(b)
	if uSteps >= 10 {
		t.Fatalf("union advanced %d runs", uSteps)
	}
	if got := u.Count(); got != 7_000_000 {
		t.Fatalf("union Count = %d, want 7M", got)
	}
	d, dSteps := a.Difference(b)
	if dSteps >= 10 {
		t.Fatalf("difference advanced %d runs", dSteps)
	}
	if got := d.Count(); got != 2_000_000 {
		t.Fatalf("difference Count = %d, want 2M", got)
	}
}

func TestOpsSelfArgument(t *testing.T) {
	a := New()
	a.SetRange(5, 50)
	u, _ := a.Union(a)
	i, _ := a.Intersect(a)
	d, _ := a.Difference(a)
	if !bytes.Equal(u.Bytes(), a.Bytes()) {
		t.Fatal("a union a != a")
	}
	if !bytes.Equal(i.Bytes(), a.Bytes()) {
		t.Fatal("a intersect a != a")
	}
	if got := d.Count(); got != 0 {
		t.Fatalf("a diff a Count = %d", got)
	}
}

func TestOpsWithEmpty(t *testing.T) {
	a := New()
	a.SetRange(5, 50)
	e := New()
	u, _ := a.Union(e)
	if !bytes.Equal(u.Bytes(), a.Bytes()) {
		t.Fatal("a union empty != a")
	}
	i, _ := a.Intersect(e)
	if got := i.Count(); got != 0 {
		t.Fatalf("a intersect empty Count = %d", got)
	}
	d, _ := a.Difference(e)
	if !bytes.Equal(d.Bytes(), a.Bytes()) {
		t.Fatal("a diff empty != a")
	}
	d2, _ := e.Difference(a)
	if got := d2.Count(); got != 0 {
		t.Fatalf("empty diff a Count = %d", got)
	}
}
