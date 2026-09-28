package temporal_test

import (
	"fmt"
	"io"
	"log"

	"ontology/temporal"
)

// ExampleJoiner 演示版本区间、朴素时态查询、缓冲与水位线推进的完整流程。
func ExampleJoiner() {
	// 关闭示例自身的噪声日志，只打印连接输出。
	j := temporal.NewJoinerWithLogger(4, log.New(io.Discard, "", 0))

	// 键 k 的版本表：
	//   [1,10)  -> "a"
	//   [10,20) -> 墓碑（无值）
	//   [20,+∞) -> "c"
	_ = j.ApplyVersion(temporal.Version{Key: "k", EffectiveAt: 1, Value: "a"})
	_ = j.ApplyVersion(temporal.Version{Key: "k", EffectiveAt: 10, Tombstone: true})
	_ = j.ApplyVersion(temporal.Version{Key: "k", EffectiveAt: 20, Value: "c"})

	submit := func(tm int64) {
		st, r, err := j.ProcessEvent(temporal.Event{Key: "k", EventTime: tm, Payload: "p"})
		if err != nil {
			fmt.Printf("event@%d rejected: %v\n", tm, err)
			return
		}
		if st == temporal.StatusResolved {
			emit(*r)
		}
	}

	submit(5)  // 缓冲（水位线 -∞）
	submit(15) // 缓冲

	out, _ := j.AdvanceWatermark(15)
	for _, r := range out {
		emit(r)
	}

	// 水位线已到 15：effectiveAt=10 的同点覆盖迟到，被拒绝且无副作用。
	err := j.ApplyVersion(temporal.Version{Key: "k", EffectiveAt: 10, Value: "late"})
	fmt.Printf("overwrite@10: %v\n", err)

	submit(25) // 缓冲
	out, _ = j.AdvanceWatermark(30)
	for _, r := range out {
		emit(r)
	}

	// output:
	// event@5 -> HIT effectiveAt=1 value=a
	// event@15 -> MISS (tombstone)
	// overwrite@10: temporal: late version change (effectiveAt <= watermark)
	// event@25 -> HIT effectiveAt=20 value=c
}

func emit(r temporal.Result) {
	switch r.Kind {
	case temporal.KindHit:
		fmt.Printf("event@%d -> HIT effectiveAt=%d value=%s\n", r.EventTime, r.EffectiveAt, r.Value)
	case temporal.KindMiss:
		fmt.Printf("event@%d -> MISS (tombstone)\n", r.EventTime)
	}
}
