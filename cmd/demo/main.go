package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"ontology/bitpack"
	"ontology/scan"
	"ontology/segment"
	"ontology/zone"
	"os"
	"slices"
)

var failures int

func check(name string, ok bool) {
	if !ok {
		failures++
	}
	fmt.Printf("%-24s %v\n", name, map[bool]string{false: "FAIL", true: "OK"}[ok])
}
func seal(rpg, card int, rows []segment.Value) []byte {
	b := segment.NewBuilder(segment.Config{RowsPerGroup: rpg, MaxDictCard: card})
	for _, r := range rows {
		_ = b.Append(r.V, r.Null)
	}
	return b.Seal()
}
func mustSeg(rpg, card int, rows []segment.Value) *segment.Segment {
	seg, _ := segment.Open(seal(rpg, card, rows))
	return seg
}
func run(seg *segment.Segment, p zone.Pred) []scan.Row {
	rows, _ := scan.Collect(scan.New(seg, p), 1<<30)
	return rows
}
func refFilter(seg *segment.Segment, p zone.Pred) (out []scan.Row) {
	vals, _ := seg.DecodeAll()
	for i, v := range vals {
		if v.Null && p.MatchNull() || !v.Null && p.Match(v.V) {
			out = append(out, scan.Row{Idx: i, V: v.V, Null: v.Null})
		}
	}
	return
}
func grid(groups, per int) (in []segment.Value) {
	for g := 0; g < groups; g++ {
		for i := 0; i < per; i++ {
			in = append(in, segment.Value{V: int64(g*per + i), Null: i%5 == 0})
		}
	}
	return
}
func mixedRows() (rows []segment.Value) {
	sp := []int64{0, 1, -1, math.MinInt64, math.MaxInt64}
	for i := 0; i < 126; i++ {
		v, null := sp[i%5], i >= 84
		if null {
			v = 0
		} else if i >= 42 {
			v = int64(i)*1e15 - 5e17
		}
		rows = append(rows, segment.Value{V: v, Null: null})
	}
	return
}
func checkBitwidth() bool {
	for _, w := range []uint8{1, 7, 8, 63, 64} {
		vals, mask := make([]uint64, 130), uint64(1)<<w-1 // 130 % 64 != 0
		for i := range vals {
			vals[i] = (uint64(i*2654435761) ^ uint64(i)<<32) & mask
		}
		if !slices.Equal(bitpack.Unpack(bitpack.Pack(vals, w), w, 130), vals) {
			return false
		}
	}
	return true
}
func checkRoundTrip() bool {
	rows := mixedRows()
	seg := mustSeg(42, 16, rows)
	ok := seg.GroupEncoding(0) == segment.Dict && seg.GroupEncoding(1) == segment.Bitpack
	got, err := seg.DecodeAll()
	return err == nil && ok && slices.Equal(got, rows)
}
func checkStates() {
	seg := mustSeg(8, 4, []segment.Value{{Null: true}, {V: 0}, {V: 5}})
	vals, err := seg.DecodeGroup(0)
	st := seg.GroupStats(0)
	three := err == nil && vals[0].Null && !vals[1].Null && vals[1].V == 0 &&
		!vals[2].Null && vals[2].V == 5 && st.Min == 0 && st.Max == 5 && st.Nulls == 1
	nullSem := true
	for _, p := range []zone.Pred{zone.Cmp(zone.Eq, 0), zone.Cmp(zone.Gt, math.MinInt64), zone.In(0, 5)} {
		nullSem = nullSem && !slices.ContainsFunc(run(seg, p), func(r scan.Row) bool { return r.Null })
	}
	ns, eq := run(seg, zone.IsNull()), run(seg, zone.Cmp(zone.Eq, 0))
	check("three-state-null", three)
	check("null-predicate-semantics", nullSem && len(ns) == 1 && ns[0].Null && len(eq) == 1 && eq[0].Idx == 1)
}
func checkPruning() {
	seg := mustSeg(16, 1024, grid(1000, 16))
	s := scan.New(seg, zone.Cmp(zone.Eq, 500*16+7))
	for g := 0; g < seg.NumGroups(); g++ { // read-only queries must not decode
		_, _ = seg.GroupStats(g), seg.GroupEncoding(g)
	}
	queryOK := s.DecodedGroups() == 0 && s.DecodedValues() == 0
	rows, _ := scan.Collect(s, 1<<30)
	check("query-no-decode", queryOK)
	check("pruning-1000-to-1", len(rows) == 1 && s.DecodedGroups() == 1 && s.DecodedValues() <= 16)
}
func checkExhaustive() bool {
	rng := rand.New(rand.NewSource(7))
	in := []segment.Value{{V: -100}, {V: 99}} // force known min/max
	for i := 0; i < 400; i++ {
		in = append(in, segment.Value{V: int64(rng.Intn(200) - 100), Null: i%11 == 0})
	}
	seg := mustSeg(13, 32, in)
	preds := []zone.Pred{zone.Cmp(zone.Eq, -100), zone.Cmp(zone.Eq, 99), zone.Cmp(zone.Gt, 99),
		zone.Cmp(zone.Lt, -100), zone.Cmp(zone.Le, -100), zone.Cmp(zone.Ge, 99), zone.In(3, 5, 500),
		zone.IsNull(), zone.IsNotNull(), zone.And(zone.Cmp(zone.Ge, -10), zone.Cmp(zone.Le, 10))}
	for _, p := range preds {
		if !slices.Equal(run(seg, p), refFilter(seg, p)) {
			return false
		}
	}
	return true
}
func checkCursorBatches() bool {
	seg := mustSeg(17, 32, grid(20, 17))
	pred := zone.And(zone.Cmp(zone.Ge, 40), zone.Cmp(zone.Le, 250))
	one := run(seg, pred)
	for l := 1; l <= seg.NumRows(); l++ {
		got, _ := scan.Collect(scan.New(seg, pred), l)
		if !slices.Equal(got, one) {
			return false
		}
	}
	return true
}
func checkTruncation() bool {
	img := seal(10, 4, mixedRows())
	for cut := 0; cut < len(img); cut++ {
		seg, err := segment.Open(img[:cut])
		for g := 0; err == nil && g < seg.NumGroups(); g++ {
			_, err = seg.DecodeGroup(g)
		}
		if err != nil && !errors.As(err, new(*segment.CorruptError)) {
			return false
		}
	}
	return true
}
func checkLimits() bool {
	cfg := segment.Config{RowsPerGroup: 2, MaxRows: 3, MaxGroups: 1, MaxDictCard: 1}
	b := segment.NewBuilder(cfg)
	_, _ = b.Append(0, false), b.Append(1, false)
	gErr := b.Append(2, false)
	cfg.MaxGroups = 5
	b2 := segment.NewBuilder(cfg)
	_, _, _ = b2.Append(0, false), b2.Append(1, false), b2.Append(2, false)
	rErr := b2.Append(9, false)
	seg, err := segment.Open(b2.Seal())
	fb := err == nil && seg.GroupEncoding(0) == segment.Bitpack && seg.GroupEncoding(1) == segment.Dict
	return errors.Is(gErr, segment.ErrTooManyGroups) && b.Len() == 2 && fb &&
		errors.Is(rErr, segment.ErrTooManyRows) && b2.Len() == 3
}
func checkConcurrent() bool {
	seg := mustSeg(25, 32, mixedRows())
	want := run(seg, zone.Cmp(zone.Ge, -1))
	bad, ok := make(chan bool, 8), true
	for w := 1; w <= 8; w++ {
		go func(l int) {
			got, _ := scan.Collect(scan.New(seg, zone.Cmp(zone.Ge, -1)), l)
			bad <- !slices.Equal(got, want)
		}(w)
	}
	for i := 0; i < 8; i++ {
		ok = ok && !<-bad
	}
	return ok
}
func main() {
	check("bitwidth-boundary", checkBitwidth())
	check("roundtrip-lossless", checkRoundTrip())
	checkStates()
	checkPruning()
	check("exhaustive-no-miss", checkExhaustive())
	check("cursor-batch-equivalence", checkCursorBatches())
	check("truncation-decidable", checkTruncation())
	check("limits-and-fallback", checkLimits())
	check("concurrent-scans", checkConcurrent())
	fmt.Printf("total: %d failure(s)\n", failures)
	os.Exit(failures)
}
