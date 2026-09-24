// Command demo runs the partial-failure merge demo checks.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"ontology/combine"
	"ontology/confidence"
	"ontology/fanout"
	"ontology/report"
	"ontology/shard"
)

var passed, total int

func check(name string, ok bool) {
	total++
	status := "FAIL"
	if ok {
		status = "OK"
		passed++
	}
	fmt.Printf("%s %s\n", status, name)
}

func checkShard() {
	f := shard.Fake{ShardID: "s1", Bound: 9, Records: []shard.Record{{ID: "a", Value: 1}}}
	r := f.Query(context.Background())
	ok := r.Claimed == 1 && len(r.Records) == 1 && r.Err == nil
	c := shard.Fake{ShardID: "s2", Corrupt: true}
	ok = ok && c.Query(context.Background()).Claimed == 1
	check("shard fake honors config (delay/corrupt/bound)", ok)
}

func makeShards(n int, fill func(i int) shard.Fake) []shard.Shard {
	out := make([]shard.Shard, n)
	for i := range out {
		f := fill(i)
		if f.ShardID == "" && f.Hang {
			f.ShardID = fmt.Sprintf("h%d", i)
		}
		out[i] = f
	}
	return out
}

func checkFanout() {
	shards := makeShards(200, func(i int) shard.Fake {
		return shard.Fake{ShardID: fmt.Sprintf("s%03d", i), Delay: 2 * time.Millisecond}
	})
	fo := fanout.New(8, 5*time.Second)
	fo.Run(context.Background(), shards)
	check("fanout peak in-flight <= limit (8)", fo.Peak() > 1 && fo.Peak() <= 8)
}

func checkDeadline() {
	var started atomic.Int64
	shards := makeShards(200, func(i int) shard.Fake {
		return shard.Fake{ShardID: fmt.Sprintf("h%03d", i), Hang: true,
			OnQuery: func() { started.Add(1) }}
	})
	fo := fanout.New(8, 50*time.Millisecond)
	begin := time.Now()
	res := fo.Run(context.Background(), shards)
	elapsed := time.Since(begin)
	ok := started.Load() <= 8 && elapsed < 50*time.Millisecond+500*time.Millisecond
	for _, r := range res {
		ok = ok && r.Err != nil
	}
	check("fanout deadline: no new launches, prompt return", ok)
}

func sampleResults() []shard.Result {
	return []shard.Result{
		{ShardID: "a", Claimed: 2, Bound: 10,
			Records: []shard.Record{{ID: "x", Value: 3}, {ID: "y", Value: 5}}},
		{ShardID: "b", Claimed: 1, Bound: 10,
			Records: []shard.Record{{ID: "z", Value: 4}}},
	}
}

func checkCombine() {
	m, err := combine.Merge(sampleResults(), 2)
	ok := err == nil && m.Count == 3 && m.Sum == 12 && m.Min == 3 && m.Max == 5 &&
		len(m.TopK) == 2 && m.TopK[0].ID == "y" && m.TopK[1].ID == "z"
	check("combine merges five aggregations", ok)

	dup := append(append([]shard.Result{}, sampleResults()...), sampleResults()[0])
	m2, _ := combine.Merge(dup, 2)
	check("combine duplicate delivery not double counted",
		m2.Count == m.Count && m2.Sum == m.Sum)

	_, err = combine.Merge([]shard.Result{{ShardID: "a", Err: errors.New("boom")}}, 1)
	check("combine all-failed returns ErrAllFailed", errors.Is(err, combine.ErrAllFailed))
}

func checkDeterminism() {
	var res []shard.Result
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("d%d", i)
		res = append(res, shard.Result{ShardID: id, Claimed: 2, Bound: 5,
			Records: []shard.Record{
				{ID: id + "a", Value: float64(i) + 0.1},
				{ID: id + "b", Value: float64(i) * 1.5}}})
	}
	want, _ := combine.Merge(res, 4)
	ok := true
	for p := 0; p < 20; p++ {
		perm := make([]shard.Result, len(res))
		for j := range res {
			dst := (j + p) % len(res)
			if p%2 == 1 {
				dst = len(res) - 1 - dst
			}
			perm[dst] = res[j]
		}
		got, _ := combine.Merge(perm, 4)
		if fmt.Sprintf("%#v", got) != fmt.Sprintf("%#v", want) {
			ok = false
		}
	}
	check("combine deterministic across 20 arrival orders", ok)
}

func partialMerge(missingBound float64) combine.Merged {
	res := sampleResults()
	res = append(res, shard.Result{ShardID: "gone", Bound: missingBound,
		Err: context.DeadlineExceeded})
	m, _ := combine.Merge(res, 3)
	return m
}

func checkConfidence() {
	full, _ := combine.Merge(sampleResults(), 3)
	allExact := true
	for _, agg := range []combine.Agg{combine.AggCount, combine.AggSum,
		combine.AggMin, combine.AggMax, combine.AggTopK} {
		allExact = allExact && confidence.Assess(agg, full).Direction == confidence.Exact
	}
	check("confidence: all-success marks all five exact", allExact)

	p := partialMerge(1)
	check("confidence: Count/Sum are lower bounds",
		confidence.Assess(combine.AggCount, p).Direction == confidence.AtLeast &&
			confidence.Assess(combine.AggSum, p).Direction == confidence.AtLeast)
	check("confidence: Min upper-bound / Max lower-bound direction",
		confidence.Assess(combine.AggMin, p).Direction == confidence.AtMost &&
			confidence.Assess(combine.AggMax, p).Direction == confidence.AtLeast)
	check("confidence: TopK trusted prefix = K on small missing bound",
		confidence.Assess(combine.AggTopK, p).TrustedPrefix == 3)
	check("confidence: TopK trusted prefix = 0 on large missing bound",
		confidence.Assess(combine.AggTopK, partialMerge(100)).TrustedPrefix == 0)
}

func checkReport() {
	r := report.Build(partialMerge(1))
	ok := len(r.Missing) == 1 && r.Missing[0] == "gone" && len(r.Shards) == 3
	for _, row := range r.Shards {
		want := "ok"
		if row.ID == "gone" {
			want = "timeout"
		}
		ok = ok && row.Status == want
	}
	ok = ok && r.Annotations[0] == ">= 3" && r.Annotations[2] == "<= 3"
	check("report: value/confidence/missing/per-shard status", ok)
}

func main() {
	checkShard()
	checkFanout()
	checkDeadline()
	checkCombine()
	checkDeterminism()
	checkConfidence()
	checkReport()
	fmt.Printf("total %d/%d passed\n", passed, total)
	if passed != total {
		os.Exit(1)
	}
}
