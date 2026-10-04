package edit

import (
	"errors"
	"math"
	"math/rand"
	"sort"
	"testing"

	"ontology/cue"
)

var exampleList = EditList{
	Deletions:  []Deletion{{2000, 5000}},
	Insertions: []Insertion{{8000, 1000}},
}

func TestRetimeSpecExamples(t *testing.T) {
	const dmin int64 = 500
	cases := []struct {
		name  string
		c     cue.Cue
		want  []cue.Cue
		drop  int
		split int
	}{
		{"left clipped", cue.Cue{Start: 1000, End: 3000, Text: "x"}, []cue.Cue{{Start: 1000, End: 2000, Text: "x"}}, 0, 0},
		{"fully deleted", cue.Cue{Start: 2500, End: 4800, Text: "x"}, nil, 1, 0},
		{"shrunk below dmin", cue.Cue{Start: 4700, End: 5400, Text: "x"}, nil, 1, 0},
		{"exact dmin kept", cue.Cue{Start: 4500, End: 5500, Text: "x"}, []cue.Cue{{Start: 2000, End: 2500, Text: "x"}}, 0, 0},
		{"internal insertion split", cue.Cue{Start: 7000, End: 9000, Text: "x"},
			[]cue.Cue{{Start: 4000, End: 5000, Text: "x"}, {Start: 6000, End: 7000, Text: "x"}}, 0, 1},
		{"insertion at start shifts", cue.Cue{Start: 8000, End: 8600, Text: "x"}, []cue.Cue{{Start: 6000, End: 6600, Text: "x"}}, 0, 0},
		{"insertion at end no extend", cue.Cue{Start: 7500, End: 8000, Text: "x"}, []cue.Cue{{Start: 4500, End: 5000, Text: "x"}}, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Retime(dmin, exampleList, []cue.Cue{tc.c})
			if err != nil {
				t.Fatal(err)
			}
			if !equalCues(got.Cues, tc.want) {
				t.Fatalf("cues got %v want %v", got.Cues, tc.want)
			}
			if got.Dropped != tc.drop || got.Splits != tc.split {
				t.Fatalf("stats got split=%d drop=%d want split=%d drop=%d",
					got.Splits, got.Dropped, tc.split, tc.drop)
			}
		})
	}
}

func TestRetimeMultiInternal(t *testing.T) {
	e := EditList{Insertions: []Insertion{{10, 100}, {20, 100}, {30, 100}}}
	got, err := Retime(1, e, []cue.Cue{{Start: 0, End: 40, Text: "z"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []cue.Cue{{Start: 0, End: 10, Text: "z"}, {Start: 110, End: 120, Text: "z"}, {Start: 220, End: 230, Text: "z"}, {Start: 330, End: 340, Text: "z"}}
	if !equalCues(got.Cues, want) || got.Splits != 3 || got.Dropped != 0 {
		t.Fatalf("got %v s=%d d=%d", got.Cues, got.Splits, got.Dropped)
	}
}

func TestDeletionThroughMiddleNoSplit(t *testing.T) {
	e := EditList{Deletions: []Deletion{{10, 30}}}
	got, err := Retime(5, e, []cue.Cue{{Start: 0, End: 40, Text: "m"}})
	if err != nil {
		t.Fatal(err)
	}
	if !equalCues(got.Cues, []cue.Cue{{Start: 0, End: 20, Text: "m"}}) {
		t.Fatalf("got %v", got.Cues)
	}
}

func TestDeletionCoversEnds(t *testing.T) {
	e := EditList{Deletions: []Deletion{{30, 50}}}
	got, _ := Retime(5, e, []cue.Cue{{Start: 0, End: 50, Text: "m"}})
	if !equalCues(got.Cues, []cue.Cue{{Start: 0, End: 30, Text: "m"}}) {
		t.Fatalf("tail got %v", got.Cues)
	}
	e2 := EditList{Deletions: []Deletion{{0, 20}}}
	got2, _ := Retime(5, e2, []cue.Cue{{Start: 0, End: 50, Text: "m"}})
	if !equalCues(got2.Cues, []cue.Cue{{Start: 0, End: 30, Text: "m"}}) {
		t.Fatalf("head got %v", got2.Cues)
	}
}

func TestInsertionAtDeletionEndpoint(t *testing.T) {
	e := EditList{
		Deletions:  []Deletion{{10, 30}},
		Insertions: []Insertion{{10, 7}, {30, 9}},
	}
	if err := Validate(e); err != nil {
		t.Fatalf("endpoint insertions should be legal: %v", err)
	}
	got, err := Retime(1, e, []cue.Cue{{Start: 0, End: 40, Text: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	// at=10,30 are internal to cue [0,40): three pieces; the middle one
	// is fully deleted and dropped with dmin=1.
	// [0,10)->[0,10); [10,30) maps to a point (dropped); [30,40)->[26,36).
	if !equalCues(got.Cues, []cue.Cue{{Start: 0, End: 10, Text: "x"}, {Start: 26, End: 36, Text: "x"}}) ||
		got.Splits != 2 || got.Dropped != 1 {
		t.Fatalf("got %v s=%d d=%d", got.Cues, got.Splits, got.Dropped)
	}
	// Same list, cue [10,30): insertions are exactly at cue ends, no split.
	got2, err2 := Retime(1, e, []cue.Cue{{Start: 10, End: 30, Text: "y"}})
	if err2 != nil {
		t.Fatal(err2)
	}
	if len(got2.Cues) != 0 || got2.Dropped != 1 {
		t.Fatalf("endpoint cue got %v d=%d", got2.Cues, got2.Dropped)
	}
	bad := EditList{Deletions: []Deletion{{10, 30}}, Insertions: []Insertion{{11, 1}}}
	if err := Validate(bad); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("interior insertion: %v", err)
	}
}

func TestValidateEditTable(t *testing.T) {
	bad := []EditList{
		{Deletions: []Deletion{{5, 5}}},
		{Deletions: []Deletion{{10, 5}}},
		{Deletions: []Deletion{{-1, 5}}},
		{Deletions: []Deletion{{0, 10}, {5, 12}}},
		{Insertions: []Insertion{{0, 0}}},
		{Insertions: []Insertion{{5, 1}, {5, 2}}},
	}
	for i, e := range bad {
		if err := Validate(e); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("case %d: want ErrInvalidParam, got %v", i, err)
		}
	}
	good := EditList{Deletions: []Deletion{{10, 30}, {30, 40}}, Insertions: []Insertion{{10, 1}, {40, 1}}}
	if err := Validate(good); err != nil {
		t.Fatalf("legal list rejected: %v", err)
	}
}

func ceilLog2(x int) int { return int(math.Ceil(math.Log2(float64(x)))) }

// bigList builds about k items on a sparse grid: deletion [4i,4i+1),
// insertion (4i+2, 1).
func bigList(k int) EditList {
	var e EditList
	n := k / 2
	for i := 0; i < n; i++ {
		e.Deletions = append(e.Deletions, Deletion{int64(4 * i), int64(4*i + 1)})
		e.Insertions = append(e.Insertions, Insertion{int64(4*i + 2), 1})
	}
	return e
}

func TestProbeBudget(t *testing.T) {
	for _, k := range []int{100, 10000} {
		e := bigList(k)
		bound := 4 * ceilLog2(k+2)
		r := newRetimer(e)
		p := int64(2*k + 10)
		r.probes = 0
		_ = r.fR(p)
		_ = r.fL(p + 5)
		used := r.probes
		if used > bound {
			t.Fatalf("k=%d piece probes=%d exceed 4*ceil(log2(k+2))=%d", k, used, bound)
		}
		t.Logf("k=%d per-piece probes=%d budget=%d", k, used, bound)

		m := 5
		r2 := newRetimer(e)
		r2.probes = 0
		pts := r2.internalInsertions(int64(4*3+1), int64(4*(3+m-1)+3))
		enumProbes := r2.probes
		if len(pts) != m {
			t.Fatalf("want %d internal points, got %d", m, len(pts))
		}
		enumBound := m + 2*ceilLog2(k+2)
		if enumProbes > enumBound {
			t.Fatalf("k=%d enum probes=%d exceed m+2*ceil(log2(k+2))=%d", k, enumProbes, enumBound)
		}
		t.Logf("k=%d enum probes=%d budget=%d", k, enumProbes, enumBound)
	}
}

func equalCues(a, b []cue.Cue) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---- naive per-millisecond simulation ----

// naiveRetime constructs the new timeline millisecond by millisecond.
// At old point p the scan order is: insertions at p, then surviving ms p.
// fL(p) is the position before inserts at p; fR(p) after inserts at p.
func naiveRetime(dmin int64, e EditList, cues []cue.Cue, bound int) Result {
	deleted := make([]bool, bound+1)
	for _, d := range e.Deletions {
		for x := int(d.A); x < int(d.B) && x < bound; x++ {
			deleted[x] = true
		}
	}
	ins := make([]int, bound+1)
	for _, in := range e.Insertions {
		if int(in.At) <= bound {
			ins[in.At] += int(in.Len)
		}
	}
	fLarr := make([]int, bound+1)
	fRarr := make([]int, bound+1)
	pos := 0
	for p := 0; p < bound; p++ {
		fLarr[p] = pos
		pos += ins[p]
		fRarr[p] = pos
		if !deleted[p] {
			pos++
		}
	}
	fLarr[bound] = pos
	pos += ins[bound]
	fRarr[bound] = pos

	res := Result{Cues: []cue.Cue{}}
	for _, c := range cues {
		bounds := []int64{c.Start}
		for _, in := range e.Insertions {
			if in.At > c.Start && in.At < c.End {
				bounds = append(bounds, in.At)
				res.Splits++
			}
		}
		bounds = append(bounds, c.End)
		for i := 0; i+1 < len(bounds); i++ {
			ns := fRarr[bounds[i]]
			ne := fLarr[bounds[i+1]]
			if int64(ne-ns) < dmin {
				res.Dropped++
				continue
			}
			res.Cues = append(res.Cues, cue.Cue{Start: int64(ns), End: int64(ne), Text: c.Text})
		}
	}
	return res
}

func insideDeletion(e EditList, at int64) bool {
	for _, d := range e.Deletions {
		if d.A < at && at < d.B {
			return true
		}
	}
	return false
}

func randomEditList(rng *rand.Rand, bound int) EditList {
	var e EditList
	used := make([]bool, bound)
	for i := 0; i < rng.Intn(4); i++ {
		a := rng.Intn(bound - 1)
		b := a + 1 + rng.Intn(bound-1-a)
		collide := false
		for x := a; x < b; x++ {
			if used[x] {
				collide = true
			}
		}
		if collide {
			continue
		}
		for x := a; x < b; x++ {
			used[x] = true
		}
		e.Deletions = append(e.Deletions, Deletion{int64(a), int64(b)})
	}
	sort.Slice(e.Deletions, func(i, j int) bool { return e.Deletions[i].A < e.Deletions[j].A })
	seenIns := map[int]bool{}
	for i := 0; i < rng.Intn(4); i++ {
		at := int64(rng.Intn(bound))
		if insideDeletion(e, at) || seenIns[int(at)] {
			continue
		}
		seenIns[int(at)] = true
		e.Insertions = append(e.Insertions, Insertion{at, int64(1 + rng.Intn(5))})
	}
	sort.Slice(e.Insertions, func(i, j int) bool { return e.Insertions[i].At < e.Insertions[j].At })
	return e
}

func randomCues(rng *rand.Rand, bound int, dmin int64) []cue.Cue {
	var cues []cue.Cue
	cur := int64(0)
	for cur+dmin < int64(bound) {
		if rng.Intn(2) == 0 {
			gap := int64(rng.Intn(3))
			cur += gap
			continue
		}
		dur := dmin + int64(rng.Intn(8))
		if cur+dur > int64(bound) {
			break
		}
		cues = append(cues, cue.Cue{Start: cur, End: cur + dur, Text: "t"})
		cur += dur
	}
	return cues
}

func TestRandomAgainstNaive(t *testing.T) {
	const bound = 80
	const n = 1500
	rng := rand.New(rand.NewSource(20261005))
	match := 0
	for iter := 0; iter < n; iter++ {
		dmin := int64(1 + rng.Intn(5))
		e := randomEditList(rng, bound)
		cues := randomCues(rng, bound, dmin)
		want := naiveRetime(dmin, e, cues, bound)
		got, err := Retime(dmin, e, cues)
		if err != nil {
			t.Fatalf("iter %d input %+v cues %+v: unexpected err %v", iter, e, cues, err)
		}
		// invariant: total output duration per cue never exceeds input duration
		totalOut := int64(0)
		for _, c := range got.Cues {
			totalOut += c.End - c.Start
		}
		totalIn := int64(0)
		for _, c := range cues {
			totalIn += c.End - c.Start
		}
		if totalOut > totalIn {
			t.Fatalf("iter %d: output duration %d > input %d", iter, totalOut, totalIn)
		}
		ok := equalCues(got.Cues, want.Cues) && got.Splits == want.Splits && got.Dropped == want.Dropped
		if ok {
			match++
		}
		if iter < 20 || !ok {
			t.Logf("iter=%d dmin=%d edits=%+v cues=%+v -> got=%+v want=%+v match=%v",
				iter, dmin, e, cues, got, want, ok)
		}
		if !ok {
			t.Fatalf("MISMATCH iter=%d (see log above)", iter)
		}
		if err := cue.Validate(dmin, got.Cues); err != nil {
			t.Fatalf("invalid output cues: %v", err)
		}
	}
	t.Logf("random comparison finished: %d/%d cases matched naive ms-by-ms model", match, n)
}
