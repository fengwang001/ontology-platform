# 使用指南：双时态快照兼容判定

## 基本用法

```go
package main

import (
	"fmt"

	"ontology/bitemporal"
	"ontology/bitemporal/judge"
	"ontology/bitemporal/registry"
)

func main() {
	eng := judge.NewEngine(registry.New())

	start, end := int64(1), int64(10)
	rec := bitemporal.Record{
		ID: "obj/prop@rev",
		Valid: &bitemporal.Interval{
			Start: &start, End: &end,
			StartClosed: bitemporal.Closed, EndClosed: bitemporal.Open, // [1,10)
		},
		Transaction: &bitemporal.Interval{
			Start: &start, End: &end,
			StartClosed: bitemporal.Closed, EndClosed: bitemporal.Open,
		},
	}

	// 仅判定
	r := eng.Judge(rec, "V2", "V1")
	fmt.Println(r.Verdict) // lossy
	for _, loss := range r.Losses {
		fmt.Println(loss.Axis, loss.Message) // valid，有效时间轴被明确具名
	}

	// 判定并执行迁移
	m, ok := eng.Migrate(rec, "V2", "V1")
	if !ok {
		// rejected / incompatible / unknown_version 不产出迁移记录
		panic(m.Result.Verdict)
	}
	fmt.Println(m.Record.Valid == nil)       // true：有效轴已丢弃
	fmt.Println(*m.Record.Transaction.End)   // 10：事务轴原样保留

	// 批量：O(n)
	_ = eng.JudgeBatch([]bitemporal.Record{rec}, "V2", "V1")

	// 连续迁移与直达对照
	cmp := eng.ComparePathWithDirect(rec, []string{"V2", "V5", "V2"})
	fmt.Println(cmp.Equivalent) // true
}
```

## 判定结果的处理建议

- `VerdictRejected`：坏数据，拒绝该记录，按 `RejectErrors` 中的具名轴回报上游；
- `VerdictIncompatible`：不要迁移，`BoundaryIssues` 给出导致归属翻转的查询时点；
- `VerdictLossy`：可以迁移，但必须把 `Losses` 持久化到迁移审计中；
  `Fills` 中的填充是固定规则行为，不需要也不接受人工确认；
- `VerdictCompatible`：信息完整保留（可能含固定填充）；
- `VerdictUnknownVersion`：版本目录需要先升级，再谈数据兼容性。

## 自定义版本

```go
reg := registry.New().Register(registry.Format{
	Version:      "VX",
	RecordsValid: true,
	RecordsTx:    true,
	ValidBounds:  bitemporal.BoundaryConvention{
		StartClosed: bitemporal.Closed, EndClosed: bitemporal.Closed},
	TxBounds: bitemporal.BoundaryConvention{
		StartClosed: bitemporal.Closed, EndClosed: bitemporal.Closed},
})
eng := judge.NewEngine(reg)
```

`Register` 返回新目录，原目录与已创建的引擎不受影响（引擎持有的是构造时的目录）。
