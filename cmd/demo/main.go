// Command demo 演示两表内连接的增量差分维护。
//
// 它按固定脚本逐批调用 join.Joiner.Apply，通过注入的 slog logger 打印：
//   - 每批输入（左、右变更）
//   - 判定依据（accepted/rejected 及可区分原因）
//   - 输出差分（仅非零、按 key/left/right 有序）
//
// 运行：go run ./cmd/demo
package main

import (
	"fmt"
	"log/slog"
	"os"

	"ontology/join"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	j := join.New(join.Options{
		MaxResultTuples: 100,
		Logger:          logger,
	})

	batches := []struct {
		name string
		b    join.Batch
	}{
		{
			name: "初始插入：左 {a:2} 右 {x:3}，连接重数 = 2×3 = 6",
			b: join.Batch{
				Left:  []join.RowChange{{Key: "k", Value: "a", Delta: 2}},
				Right: []join.RowChange{{Key: "k", Value: "x", Delta: 3}},
			},
		},
		{
			name: "同时改两张表：左删 a 增 b，右增 y",
			b: join.Batch{
				Left:  []join.RowChange{{Key: "k", Value: "a", Delta: -1}, {Key: "k", Value: "b", Delta: 1}},
				Right: []join.RowChange{{Key: "k", Value: "y", Delta: 2}},
			},
		},
		{
			name: "非法批（删除不存在的行，批后负重数）：整批拒绝，状态不变",
			b: join.Batch{
				Left:  []join.RowChange{{Key: "k", Value: "b", Delta: -99}},
				Right: []join.RowChange{{Key: "k", Value: "x", Delta: 1}},
			},
		},
		{
			name: "非法批（空值）：整批拒绝",
			b: join.Batch{
				Left: []join.RowChange{{Key: "", Value: "a", Delta: 1}},
			},
		},
		{
			name: "非法批（零增量）：整批拒绝",
			b: join.Batch{
				Right: []join.RowChange{{Key: "k", Value: "x", Delta: 0}},
			},
		},
		{
			name: "合法删除：撤销右行 x，输出负重数差分",
			b: join.Batch{
				Right: []join.RowChange{{Key: "k", Value: "x", Delta: -3}},
			},
		},
	}

	for i, bc := range batches {
		fmt.Printf("---- batch %d: %s ----\n", i+1, bc.name)
		res := j.Apply(bc.b)
		if res.Accepted {
			fmt.Printf("decision: accepted, %d non-zero delta tuple(s)\n", len(res.Deltas))
		} else {
			fmt.Printf("decision: rejected, reason=%s detail=%s\n", res.Err.Reason, res.Err.Error())
		}
		// 每批后用全量重算交叉校验物化视图（demo 自检）。
		left, right, mat := j.Snapshot()
		full := join.FullJoin(left, right)
		if !mapsEqual(mat, full) {
			fmt.Printf("INTERNAL ERROR: materialized view diverges from full recompute\n")
			os.Exit(1)
		}
		fmt.Printf("materialized result now has %d distinct tuple(s)\n\n", len(mat))
	}

	fmt.Println("OK: materialized view matched full recompute after every batch")
}

func mapsEqual(a, b map[join.JoinTuple]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
