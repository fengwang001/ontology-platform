# recall — 医院药品批次召回与追溯锁定

无外部依赖的 Go 包。入口为 `recall.System`，方法级线程安全，所有操作
互斥串行，拒绝的操作不改变任何状态与时钟。

## 操作一览

| 方法 | 说明 |
| --- | --- |
| `Inbound` | 药品/批号/数量入库进药库；同药品批号不可重复入库 |
| `Transfer` | 两位置间调拨，受有效等级约束 |
| `Dispense` | 从某位置向患者发放；三级召回须 `Consent=true` |
| `Return` | 患者退药，只进药库；按 FIFO（时刻、同刻按先后）抵扣 |
| `RegisterRecall` | 登记批号闭区间召回（等级 1 最严），登记即生效、覆盖未来入库 |
| `ReleaseRecall` | 解除单条召回 |
| `QueryBatch` | 各位置库存、有效等级、并列最严召回编号集合 |
| `RecoveryList` | 一/二级召回的追回清单（按患者×批次汇总未退回数量） |

位置：药库为保留标识 `recall.Warehouse`（"药库"），其余非空字符串均为
病区药柜。错误统一为 `*recall.OpError`，按
参数非法 → 时钟回退 → 对象不存在 → 召回禁止 → 需知情确认 →
库存不足 → 退药超量 → 状态不符
的优先级只报第一个。

## 快速示例

```go
s := recall.New()
_ = s.Inbound(recall.InboundReq{Now: 1, DrugID: "D1", BatchID: "B001", Quantity: 100})
_ = s.RegisterRecall(recall.RegisterRecallReq{
    Now: 2, RecallID: "R1", DrugID: "D1",
    LotLow: "B001", LotHigh: "B999", Level: 1, IssueAt: 0,
})
err := s.Dispense(recall.DispenseReq{Now: 3, DrugID: "D1", BatchID: "B001",
    Location: recall.Warehouse, Patient: "P1", Quantity: 1})
// err 为 *recall.OpError，Code == recall.CodeRecallForbidden
```

## 测试

```bash
go test ./recall/ -v                       # 规格用例
go test ./recall/ -run TestRandomDifferential -v   # 1500 组朴素模型差分
go test ./recall/ -run TestScaleComparison -v      # 两档规模对照
go test -race ./recall/                    # 并发串行化与数据竞争
go test -bench . -benchmem ./recall/
```

差分逐步日志：`/tmp/recall_diff_trace.log`（每步输入、两侧输出、判定依据）。
设计取舍与放弃方案见 `DESIGN.md`。
