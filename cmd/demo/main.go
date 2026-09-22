// Demo exercises every guarantee of the segment/scanner packages
// and prints one OK/FAIL line per check plus a final summary.
package main

import (
	"fmt"
	"math/rand"
	"os"

	"ontology/scan"
	"ontology/segment"
	"ontology/zone"
)

var failures int

func report(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%-28s %s\n", name, status)
}

func build(groups []struct {
	vals  []int64
	nulls []bool
}) *segment.Segment {
	b := segment.NewBuilder(segment.Config{MaxDictCard: 4})
	for _, g := range groups {
		if err := b.AddRowGroup(g.vals, g.nulls); err != nil {
			panic(err)
		}
	}
	seg, err := segment.Open(b.Bytes())
	if err != nil {
		panic(err)
	}
	return seg
}

func grp(vals ...int64) struct {
	vals  []int64
	nulls []bool
} {
	return struct {
		vals  []int64
		nulls []bool
	}{vals, make([]bool, len(vals))}
}

func main() {
	checkRoundTrip()
	checkThreeStates()
	checkPruning()
	checkExhaustive()
	checkNullSemantics()
	checkBatches()
	checkTruncation()
	checkWidths()
	checkLimits()
	checkMetadata()
	checkConcurrency()
	fmt.Printf("total: %d checks, %d failed\n", 11, failures)
	if failures > 0 {
		os.Exit(1)
	}
}

func checkRoundTrip() {
	g1 := grp(5, 5, -2, 0, 7)               // dict path
	g2 := grp(1<<62, -1<<62, 3, 9, 100, -7) // bitpack path
	g1.nulls[2] = true
	seg := build([]struct {
		vals  []int64
		nulls []bool
	}{g1, g2})
	ok := seg.GroupEncoding(0) == segment.DictEncoded &&
		seg.GroupEncoding(1) == segment.BitPacked
	for i, g := range []struct {
		vals  []int64
		nulls []bool
	}{g1, g2} {
		v, n, err := seg.DecodeRowGroup(i)
		if err != nil {
			ok = false
			continue
		}
		for j := range g.vals {
			if n[j] != g.nulls[j] || (!n[j] && v[j] != g.vals[j]) {
				ok = false
			}
		}
	}
	report("roundtrip both encodings", ok)
}

func checkThreeStates() {
	g := grp(0, 0, 1)
	g.nulls[1] = true
	seg := build([]struct {
		vals  []int64
		nulls []bool
	}{g})
	v, n, _ := seg.DecodeRowGroup(0)
	ok := !n[0] && v[0] == 0 && n[1] && !n[2] && v[2] == 1
	st := seg.GroupStats(0)
	ok = ok && st.Min == 0 && st.Max == 1 && st.Nulls == 1
	report("null vs zero distinct", ok)
}

func checkPruning() {
	var groups []struct {
		vals  []int64
		nulls []bool
	}
	for i := 0; i < 1000; i++ {
		row := grp()
		for j := 0; j < 100; j++ {
			row.vals = append(row.vals, int64(i))
			row.nulls = append(row.nulls, false)
		}
		groups = append(groups, row)
	}
	seg := build(groups)
	rows := scan.NewScanner(seg, []zone.Pred{zone.Equal(537)}).All()
	ok := len(rows) == 100 && seg.DecodedGroups() == 1 && seg.DecodedValues() <= 100
	report("pruning 1000->1 group", ok)
}

func checkExhaustive() {
	rng := rand.New(rand.NewSource(1))
	var groups []struct {
		vals  []int64
		nulls []bool
	}
	var vals []int64
	var nulls []bool
	for gi := 0; gi < 30; gi++ {
		gr := grp()
		for j := 0; j < 1+rng.Intn(30); j++ {
			v := int64(rng.Intn(41) - 20)
			nu := rng.Intn(5) == 0
			gr.vals, gr.nulls = append(gr.vals, v), append(gr.nulls, nu)
			vals, nulls = append(vals, v), append(nulls, nu)
		}
		groups = append(groups, gr)
	}
	seg := build(groups)
	mn, mx := int64(1<<62), int64(-1<<62)
	for i, v := range vals {
		if !nulls[i] {
			mn, mx = min(mn, v), max(mx, v)
		}
	}
	preds := [][]zone.Pred{
		{zone.Equal(mn)}, {zone.Equal(mx)}, {zone.GreaterThan(mx)},
		{zone.LessThan(mn)}, {zone.GreaterEqual(mn), zone.LessEqual(mx)},
		{zone.Null()}, {zone.NotNull()}, {zone.InSet(mn, mx)},
	}
	ok := true
	for _, ps := range preds {
		got := scan.NewScanner(seg, ps).All()
		n := 0
		for i, v := range vals {
			if zone.MatchAll(v, nulls[i], ps) {
				if n >= len(got) || got[n].Null != nulls[i] ||
					(!nulls[i] && got[n].Value != v) {
					ok = false
				}
				n++
			}
		}
		ok = ok && n == len(got)
	}
	report("exhaustive prune equivalence", ok)
}

func checkNullSemantics() {
	g := grp(0, 5, 0)
	g.nulls[2] = true
	seg := build([]struct {
		vals  []int64
		nulls []bool
	}{g})
	ok := true
	for _, ps := range [][]zone.Pred{{zone.Equal(0)}, {zone.LessThan(9)}, {zone.NotNull()}} {
		for _, r := range scan.NewScanner(seg, ps).All() {
			ok = ok && !r.Null
		}
	}
	rows := scan.NewScanner(seg, []zone.Pred{zone.Null()}).All()
	ok = ok && len(rows) == 1 && rows[0].Null
	report("null matches only IS NULL", ok)
}
