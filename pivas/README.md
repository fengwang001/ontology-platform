# pivas — 静脉用药配置中心排程与稳定期判定

设计取舍、被放弃方案与验证方法见 `docs/DESIGN.md`。

## 快速开始

```go
c, _ := pivas.NewCenter(5, 8) // 室温/冷藏运送时长（秒）

// 目录登记（仅对之后受理的医嘱生效）
c.UpsertDrug(0, pivas.Drug{ID: "A", RoomStableSec: 100, ColdStableSec: 300, SolventClass: "NS"})
c.UpsertDrug(0, pivas.Drug{ID: "B", RoomStableSec: 100, ColdStableSec: 300, LightSensitive: true, SolventClass: "NS"})
c.AddIncompatibility(0, "A", "C")

// 洁净台：容量 2，数量→时长对照严格递增，清场间隔 10 秒
c.RegisterBench(0, pivas.BenchConfig{
    ID: "B1", Capacity: 2, DurationByCount: []int64{0, 10, 20}, ClearanceSec: 10,
})

// 受理：给出存放方式、洁净台、批次与送达时刻
a, err := c.Admit(0, pivas.Order{
    ID: "o1", DrugIDs: []string{"A"}, Solvent: "NS",
    RequiredAt: 1000, LightProofBag: false,
})

// 取消尚未开始的医嘱
err = c.Cancel(0, "o1")
```

## 操作一览

| 方法 | 说明 |
| --- | --- |
| `NewCenter(roomT, coldT)` | 创建系统，运送时长按存放方式配置 |
| `UpsertDrug(now, drug)` | 登记/变更药品，不追溯已受理医嘱 |
| `AddIncompatibility` / `RemoveIncompatibility` | 登记/移除无序禁忌配对 |
| `RegisterBench(now, cfg)` | 登记洁净台（编号唯一） |
| `Admit(now, order)` | 受理医嘱，返回 `Admission` 或分类错误 |
| `Cancel(now, orderID)` | 取消未开始医嘱 |
| `DumpState()` | 确定性状态快照（重放审计） |
| `VerifyInvariants()` | 校验按时/有效期/编组等不变量 |

## 错误码（按上报优先级）

`CodeInvalidParam` > `CodeClockRollback` > `CodeDrugNotFound` > `CodeIncompatiblePair` >
`CodeSolventMismatch` > `CodeLightConflict` > `CodeNoFeasibleSlot`，另有
`CodeStateConflict`（取消已开始/已取消医嘱）与 `CodeOrderNotFound`（取消未知医嘱）。
所有错误均为 `*pivas.Error`，可用 `errors.As` 取出 `Code` 判定。

## 测试与基准

```bash
go test ./pivas/
go test -race ./pivas/
go test ./pivas/ -run XXX -bench . -benchtime 1000000x
```
