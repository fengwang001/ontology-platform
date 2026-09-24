package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"ontology/combine"
	"ontology/fanout"
	"ontology/report"
	"ontology/shard"
)

var pass, fail int

func check(name string, ok bool) {
	if ok {
		pass++
		fmt.Println("OK   " + name)
	} else {
		fail++
		fmt.Println("FAIL " + name)
	}
}

func run(cfgs []shard.Config, d time.Duration) (*fanout.Result, error) {
	ss := make([]shard.Shard, len(cfgs))
	for i := range cfgs {
		ss[i] = shard.New(cfgs[i])
	}
	return fanout.Run(context.Background(), ss, 8, d)
}

func main() {
	recs := func(base string, n int) []shard.Record {
		rs := make([]shard.Record, n)
		for i := 0; i < n; i++ {
			rs[i] = shard.Record{ID: fmt.Sprintf("%s%d", base, i), V: float64(n - i)}
		}
		return rs
	}

	allOK, _ := run([]shard.Config{
		{ShardID: "a", Records: recs("a", 3)},
		{ShardID: "b", Records: recs("b", 3)},
	}, time.Second)
	rep := report.Build(allOK, 3)
	exact := rep.Labels[combine.Count].Exact && rep.Labels[combine.Min].Exact &&
		rep.Labels[combine.Max].Exact && rep.Labels[combine.Sum].Exact &&
		rep.Labels[combine.TopK].Exact
	check("all-success labels all five exact", exact)

	oneMissing, _ := run([]shard.Config{
		{ShardID: "a", Records: recs("a", 3)},
		{ShardID: "b", Hang: true},
	}, 120*time.Millisecond)
	mr := report.Build(oneMissing, 3)
	check("missing: Count/Sum lower bound",
		strings.HasPrefix(mr.Labels[combine.Count].Text, "Count >=") &&
			strings.HasPrefix(mr.Labels[combine.Sum].Text, "Sum >="))
	check("missing: Min upper-only (<=), Max lower-only (>=)",
		strings.HasPrefix(mr.Labels[combine.Min].Text, "Min <=") &&
			strings.HasPrefix(mr.Labels[combine.Max].Text, "Max >="))

	small := []shard.Config{{ShardID: "a", Records: recs("a", 5)}}
	for i, b := range []float64{0.5, 1000} {
		f := shard.New(shard.Config{ShardID: "z", Hang: true})
		f.SetBound(b)
		fss := []shard.Shard{shard.New(small[0]), f}
		r, _ := fanout.Run(context.Background(), fss, 2, 100*time.Millisecond)
		p := report.Build(r, 3).Labels[combine.TopK].Prefix
		if i == 0 {
			check("TopK prefix=K when missing bound tiny", p == 3)
		} else {
			check("TopK prefix=0 when missing bound huge", p == 0)
		}
	}

	many := make([]shard.Config, 200)
	for i := range many {
		many[i] = shard.Config{ShardID: fmt.Sprintf("s%03d", i), Records: recs("x", 1)}
	}
	big, _ := run(many, 2*time.Second)
	check("peak in-flight <= cap 8", big.PeakInFlight() <= 8 && big.PeakInFlight() > 0)

	dup, _ := run([]shard.Config{
		{ShardID: "a", Records: recs("a", 3), Duplicate: true},
		{ShardID: "b", Records: recs("b", 3)},
	}, time.Second)
	once, _ := run([]shard.Config{
		{ShardID: "a", Records: recs("a", 3)},
		{ShardID: "b", Records: recs("b", 3)},
	}, time.Second)
	check("duplicated chunks not double counted",
		combine.All(dup, 3).Count == combine.All(once, 3).Count &&
			combine.All(dup, 3).Sum == combine.All(once, 3).Sum)

	_, errAll := run([]shard.Config{{ShardID: "a", Hang: true}, {ShardID: "b", Fail: true}}, 100*time.Millisecond)
	check("all shards failed returns typed error", errors.Is(errAll, fanout.ErrAllFailed))

	_, errZero := fanout.Run(context.Background(), nil, 1, time.Second)
	check("zero shards returns typed error", errors.Is(errZero, fanout.ErrNoShards))

	empty, _ := run([]shard.Config{{ShardID: "a"}, {ShardID: "b"}}, time.Second)
	check("empty success differs from all-failed", report.Build(empty, 3).Labels[combine.Count].Exact)

	c1 := []shard.Config{{ShardID: "a", Records: recs("a", 2)}, {ShardID: "b", Records: recs("b", 2)}}
	c2 := []shard.Config{c1[1], c1[0]}
	r1, _ := run(c1, time.Second)
	r2, _ := run(c2, time.Second)
	check("arrival-order byte-identical report", report.Build(r1, 3).String() == report.Build(r2, 3).String())

	fmt.Printf("TOTAL pass=%d fail=%d\n", pass, fail)
}
