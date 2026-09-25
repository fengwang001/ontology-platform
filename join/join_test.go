package join

import (
	"fmt"
	"math/rand"
	"slices"
	"testing"

	"ontology/nkey"
)

// TestNullIsolation pins invariant 2: NULL matches nothing, not even NULL.
func TestNullIsolation(t *testing.T) {
	cases := []struct {
		name string
		a, b *string
	}{
		{"both nil", nil, nil},
		{"nil left", nil, ptr("k")},
		{"nil right", ptr("k"), nil},
	}
	for _, c := range cases {
		if nkey.Matches(c.a, c.b) {
			t.Errorf("%s: NULL must never match", c.name)
		}
	}
	e := New()
	e.Feed(L, nil, 1)
	e.Feed(R, nil, 2)
	e.Feed(L, ptr("k"), 3)
	if n := len(e.Snapshot()); n != 0 {
		t.Fatalf("NULL rows produce no outputs before any match; got %d", n)
	}
	e.Feed(R, ptr("k"), 4)
	if n := len(e.Snapshot()); n != 1 {
		t.Fatalf("NULL rows must never produce outputs; got %d", n)
	}
}

// TestEmptyStringDistinctFromNull pins the "" vs nil distinction.
func TestEmptyStringDistinctFromNull(t *testing.T) {
	if !nkey.Matches(ptr(""), ptr("")) {
		t.Fatal(`"" must match ""`)
	}
	if nkey.Matches(ptr(""), nil) || nkey.Matches(nil, ptr("")) {
		t.Fatal(`"" must not match NULL`)
	}
	e := New()
	e.Feed(R, nil, 1)
	e.Feed(L, ptr(""), 2)
	if len(e.Snapshot()) != 0 {
		t.Fatal(`L("") must not join the buffered NULL row`)
	}
	e.Feed(R, ptr(""), 3)
	o := e.Snapshot()
	if len(o) != 1 || o[0].Key != "" || o[0].LVal != 2 || o[0].RVal != 3 {
		t.Fatalf(`"" joins only "", got %+v`, o)
	}
}

// TestNestedLoopEquivalence pins invariant 1 against a naive nested loop
// over many generated random interleavings (table of seeds, looped).
func TestNestedLoopEquivalence(t *testing.T) {
	// Two distinct pointers to the same value also check that indexing is
	// by string value, not pointer identity.
	pool := []*string{nil, ptr(""), ptr("a"), ptr("b"), ptr("a")}
	for seed := int64(0); seed < 50; seed++ {
		rng := rand.New(rand.NewSource(seed))
		e := New()
		var ll, rr []Row
		for step := 0; step < 80; step++ {
			sd, key, v := Side(rng.Intn(2)), pool[rng.Intn(len(pool))], rng.Int63n(100)
			before := len(e.Snapshot())
			e.Feed(sd, key, v)
			if got, want := e.Snapshot()[before:], naive(ll, rr, sd, key, v); !slices.Equal(got, want) {
				t.Fatalf("seed %d step %d: got %+v want %+v", seed, step, got, want)
			}
			if sd == L {
				ll = append(ll, Row{key, v})
			} else {
				rr = append(rr, Row{key, v})
			}
		}
	}
}

// TestMultiplicity pins invariant 3: duplicates are kept and pair up
// fully (nL x nR), outputs follow matched-row insertion order.
func TestMultiplicity(t *testing.T) {
	cases := []struct{ nL, nR int }{
		{1, 1}, {2, 3}, {3, 3}, {5, 2},
	}
	for _, c := range cases {
		e := New()
		k := "k"
		for i := 0; i < c.nR; i++ {
			e.Feed(R, &k, int64(100+i))
		}
		for i := 0; i < c.nL; i++ {
			e.Feed(L, &k, int64(i))
		}
		o := e.Snapshot()
		if len(o) != c.nL*c.nR {
			t.Errorf("%dx%d: got %d outputs want %d", c.nL, c.nR, len(o), c.nL*c.nR)
		}
		if o[0].RVal != 100 || o[c.nR-1].RVal != int64(100+c.nR-1) {
			t.Errorf("%dx%d: first L event must match R rows in insertion order", c.nL, c.nR)
		}
	}
}

// TestProbeCounter pins section 4: with m distinct opposite rows and one
// true hit, the unexported probe counter must stay == hits regardless of
// m, proving hash-index lookup rather than a full scan.
func TestProbeCounter(t *testing.T) {
	for _, m := range []int{100, 1000, 5000, 10000} {
		e := New()
		for i := 0; i < m; i++ {
			s := fmt.Sprintf("key-%05d", i)
			e.Feed(R, &s, int64(i))
		}
		hit := fmt.Sprintf("key-%05d", m/3)
		e.Feed(L, &hit, 9)
		if e.lastProbed != 1 { // small constant(0) + real hits(1); independent of m
			t.Errorf("m=%d: probed %d rows, want exactly the 1 hit", m, e.lastProbed)
		}
		if o := e.Snapshot(); len(o) != 1 || o[0].RVal != int64(m/3) {
			t.Errorf("m=%d: wrong hit output %+v", m, o)
		}
		e.Feed(L, nil, 1)
		if e.lastProbed != 0 || len(e.Snapshot()) != 1 {
			t.Errorf("m=%d: NULL key must probe 0 rows", m)
		}
	}
}
