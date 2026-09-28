package temporaljoin_test

import (
	"errors"
	"fmt"

	"ontology/temporaljoin"
)

// ExampleJoiner 展示完整的时态连接流程：版本区间、左闭右开、墓碑、
// 事件缓冲与水位线推进，以及迟到版本变更的拒绝。
func ExampleJoiner() {
	// 静默组件自身的结构化日志，Example 只展示语义输入与输出。
	j, err := temporaljoin.NewJoiner(temporaljoin.Options{BufferCapacity: 16})
	if err != nil {
		panic(err)
	}

	// 键 k 的版本表（区间左闭右开，最后一个版本到无穷）：
	//   [1,2) -> "v1"
	//   [2,5) -> 墓碑（无值）
	//   [5,∞) -> "v5"
	mustOK(j.PutVersion("k", 1, []byte("v1")))
	mustOK(j.PutTombstone("k", 2))
	mustOK(j.PutVersion("k", 5, []byte("v5")))

	// 事件先缓冲：它们会在水位线关闭对应时间点时才被连接。
	for _, at := range []int64{0, 1, 2, 4, 5, 9} {
		_, err := j.PutEvent("k", at, nil)
		mustOK(err)
	}

	// 推进水位线，一次性输出全部事件，结果按（事件时间, 序号）升序。
	results, err := j.AdvanceWatermark(9)
	mustOK(err)
	for _, r := range results {
		if r.Hit {
			fmt.Printf("event@%d -> %s value=%q (version@%d)\n", r.EventTime, r.Basis, string(r.Value), r.VersionEffectiveAt)
		} else {
			fmt.Printf("event@%d -> %s miss\n", r.EventTime, r.Basis)
		}
	}

	// 水位线已到 9：生效起点不晚于 9 的版本变更一律拒绝（表不变）。
	err = j.PutVersion("k", 9, []byte("too-late"))
	var rejected *temporaljoin.RejectError
	if errors.As(err, &rejected) {
		fmt.Printf("rejected: %s\n", rejected.Reason)
	}

	// 水位线回退同样被拒绝。
	_, err = j.AdvanceWatermark(8)
	if errors.As(err, &rejected) {
		fmt.Printf("rejected: %s\n", rejected.Reason)
	}

	// Output:
	// event@0 -> no_version miss
	// event@1 -> hit value="v1" (version@1)
	// event@2 -> tombstone miss
	// event@4 -> tombstone miss
	// event@5 -> hit value="v5" (version@5)
	// event@9 -> hit value="v5" (version@5)
	// rejected: late_version_change
	// rejected: watermark_regression
}

func mustOK(err error) {
	if err != nil {
		panic(err)
	}
}
