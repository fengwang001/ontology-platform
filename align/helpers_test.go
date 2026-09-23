package align

import "sort"

import "ontology/agg"
import "ontology/point"

func sortPoints(ps []point.Point) {
	sort.SliceStable(ps, func(i, j int) bool { return ps[i].TS < ps[j].TS })
}

func sortBuckets(bs []agg.Bucket) {
	sort.Slice(bs, func(i, j int) bool { return bs[i].Start < bs[j].Start })
}

func concat(base []point.Point, copies int) []point.Point {
	out := make([]point.Point, 0, len(base)*copies)
	for i := 0; i < copies; i++ {
		out = append(out, base...)
	}
	return out
}
