// Command demo 演示分片扇出查询在部分失败下的带可信度合并。
package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"time"

	"ontology/combine"
	"ontology/confidence"
	"ontology/fanout"
	"ontology/report"
	"ontology/shard"
)

var passed, failed int

func check(name string, ok bool, detail string) {
	if ok {
		passed++
		fmt.Printf("OK   %s %s\n", name, detail)
	} else {
		failed++
		fmt.Printf("FAIL %s %s\n", name, detail)
	}
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	hang := shard.NewFake("hang", 1, nil, shard.WithHang())
	_, err := hang.Query(ctx)
	corrupt := shard.NewFake("bad", 1, []shard.Record{{ID: "a", Value: 1}}, shard.WithCorrupt())
	resp, _ := corrupt.Query(context.Background())
	check("shard: 挂起可取消/损坏可注入", err != nil && resp.Claimed != len(resp.Records), "")

	many := make([]shard.Shard, 200)
	for i := range many {
		many[i] = shard.NewFake(fmt.Sprintf("s%03d", i), 1,
			[]shard.Record{{ID: "r", Value: 1}}, shard.WithDelay(time.Millisecond))
	}
	f := fanout.New(8)
	f.Run(context.Background(), many, 10*time.Second)
	check("fanout: 并发峰值<=上限", f.Peak() <= 8, fmt.Sprintf("(peak=%d/8)", f.Peak()))

	merged := combine.Merge([]shard.Response{
		{ShardID: "s1", Records: []shard.Record{{ID: "a", Value: 5}, {ID: "b", Value: 3}}, Claimed: 2},
		{ShardID: "s2", Records: []shard.Record{{ID: "a", Value: 2}, {ID: "c", Value: 9}}, Claimed: 2},
	}, 2)
	check("combine: 五种聚合合并", merged.Count == 4 && merged.Sum == 19 &&
		merged.Min == 2 && merged.Max == 9 &&
		merged.TopK[0].ID == "c" && merged.TopK[1].ID == "a", "")

	part := confidence.Input{Total: 4, OK: 3, MissingBound: 5, Value: 42,
		TopK: []combine.TopEntry{{ID: "a", Score: 100}, {ID: "b", Score: 90}, {ID: "c", Score: 80}}}
	ca, sa := confidence.AssessCount(part), confidence.AssessSum(part)
	check("confidence: Count/Sum 为下界", ca.Kind == confidence.LowerBound && sa.Kind == confidence.LowerBound &&
		ca.HiInf && sa.HiInf, fmt.Sprintf("(%s / %s)", ca.Note, sa.Note))
	ma, xa := confidence.AssessMin(part), confidence.AssessMax(part)
	check("confidence: Min 上界/Max 下界", ma.Kind == confidence.UpperBound && ma.Hi == 42 &&
		xa.Kind == confidence.LowerBound && xa.Lo == 42, fmt.Sprintf("(%s / %s)", ma.Note, xa.Note))
	smallB := confidence.AssessTopK(part)
	bigB := confidence.AssessTopK(confidence.Input{Total: 4, OK: 3, MissingBound: 1000, TopK: part.TopK})
	check("confidence: TopK 可信前缀 K 与 0", smallB.TrustedPrefix == 3 && bigB.TrustedPrefix == 0,
		fmt.Sprintf("(%s / %s)", smallB.Note, bigB.Note))

	full := []shard.Shard{
		shard.NewFake("s1", 100, []shard.Record{{ID: "a", Value: 60}, {ID: "b", Value: 50}}),
		shard.NewFake("s2", 100, []shard.Record{{ID: "a", Value: 40}, {ID: "c", Value: 30}}),
	}
	rep, _ := buildReport(full, 3)
	allExact := rep.Count.Kind == confidence.Exact && rep.Sum.Kind == confidence.Exact &&
		rep.Min.Kind == confidence.Exact && rep.Max.Kind == confidence.Exact &&
		rep.TopK.Kind == confidence.Exact
	check("report: 全部成功五种聚合均精确", allExact, "")

	dup := shard.NewFake("dup", 10, []shard.Record{{ID: "a", Value: 5}, {ID: "b", Value: 3}})
	once, _ := buildReport([]shard.Shard{dup}, 2)
	twice, _ := buildReport([]shard.Shard{dup, dup}, 2)
	check("report: 重复返回不重复计数", once.Combined.Count == twice.Combined.Count &&
		once.Combined.Sum == twice.Combined.Sum,
		fmt.Sprintf("(count=%d sum=%v)", twice.Combined.Count, twice.Combined.Sum))

	_, allErr := buildReport([]shard.Shard{shard.NewFake("x", 1, nil, shard.WithHang())}, 2)
	check("report: 全部失败返回错误", errors.Is(allErr, report.ErrAllFailed), "")

	det := true
	base, _ := buildReport(append(full, shard.NewFake("bad", 1, nil, shard.WithCorrupt())), 3)
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 20; i++ {
		shuffled := append([]shard.Shard{}, full...)
		shuffled = append(shuffled, shard.NewFake("bad", 1, nil, shard.WithCorrupt()))
		rng.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })
		got, _ := buildReport(shuffled, 3)
		if !reflect.DeepEqual(got.Combined, base.Combined) || !reflect.DeepEqual(got.Missing, base.Missing) {
			det = false
		}
	}
	check("report: 打乱到达顺序结果一致", det, fmt.Sprintf("(missing=%v)", base.Missing))

	fmt.Printf("TOTAL %d/%d OK\n", passed, passed+failed)
}

func buildReport(shards []shard.Shard, k int) (report.Report, error) {
	results := fanout.New(8).Run(context.Background(), shards, 200*time.Millisecond)
	return report.Build(shards, results, k)
}
