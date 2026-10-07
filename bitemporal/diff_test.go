package bitemporal

import (
	"math/rand"
	"testing"
)

// TestDifferentialAgainstNaive runs many random operation sequences against
// both the production exporter and the independent O(N) naive model, asserting
// agreement for point visibility and timeline segments at multiple cutoffs.
func TestDifferentialAgainstNaive(t *testing.T) {
	for seed := int64(1); seed <= 40; seed++ {
		rng := rand.New(rand.NewSource(seed))
		s := NewStore(-50, -50)
		if err := s.RegisterSchema("p", -50, map[string]FieldKind{"v": KindInt}); err != nil {
			t.Fatal(err)
		}
		exp := NewExporter(s, NopLogger{})
		naive := NewNaiveModel()

		objects := []string{"x", "y", "z"}
		ops := rng.Intn(120) + 20
		tick := Tick(-40)
		for i := 0; i < ops; i++ {
			oid := objects[rng.Intn(len(objects))]
			start := Tick(rng.Intn(60) - 30)
			length := rng.Intn(10) + 1
			end := start + Tick(length)
			iv, err := NewInterval(start, end)
			if err != nil {
				continue
			}
			s.AdvanceClock(tick)
			tick += Tick(rng.Intn(3)) // non-decreasing transaction clock
			rec, err := s.Write(oid, "p", iv, Value{Fields: map[string]any{"v": int64(i)}})
			if err != nil {
				t.Fatalf("seed=%d write: %v", seed, err)
			}
			naive.Put(*rec)
		}

		// Query at several transaction cutpoints, including exact write times.
		for _, t0 := range []Tick{-40, -20, 0, 20, tick, tick + 5} {
			c, err := exp.Freeze(t0)
			if err != nil {
				t.Fatalf("seed=%d freeze: %v", seed, err)
			}
			for _, oid := range objects {
				// Point agreement over a dense probe grid.
				for v := Tick(-32); v <= 32; v++ {
					got, _ := exp.VisibleAt(c, oid, v)
					want := naive.PointVisible(oid, v, c)
					if (got == nil) != (want == nil) {
						t.Fatalf("seed=%d t=%d obj=%s v=%d presence mismatch got=%v want=%v", seed, t0, oid, v, got, want)
					}
					if got != nil && got.Seq != want.Seq {
						t.Fatalf("seed=%d t=%d obj=%s v=%d seq mismatch got=%d want=%d", seed, t0, oid, v, got.Seq, want.Seq)
					}
				}
				// Timeline agreement on a bounded window.
				span := Interval{Start: -32, End: 33}
				snap, err := exp.ExportObject(c, "p", oid, &span)
				if err != nil {
					t.Fatalf("seed=%d export: %v", seed, err)
				}
				wantSegs := naive.TimelineVisible(oid, span, c)
				if len(snap.Segments) != len(wantSegs) {
					t.Fatalf("seed=%d t=%d obj=%s segment count mismatch %d vs %d: %+v vs %+v",
						seed, t0, oid, len(snap.Segments), len(wantSegs), snap.Segments, wantSegs)
				}
				for i, g := range snap.Segments {
					w := wantSegs[i]
					if g.Start != w.Start || g.End != w.End {
						t.Fatalf("seed=%d segment bounds mismatch at %d: %+v vs %+v", seed, i, g, w)
					}
					gotSeq := uint64(0)
					if g.Value != nil {
						if r, _ := exp.VisibleAt(c, oid, g.Start); r != nil {
							gotSeq = r.Seq
						}
					}
					if gotSeq != w.Seq {
						t.Fatalf("seed=%d segment value mismatch: gotSeq=%d wantSeq=%d", seed, gotSeq, w.Seq)
					}
				}
			}
		}
	}
}
