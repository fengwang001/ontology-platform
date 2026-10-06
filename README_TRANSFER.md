# 在途库存调拨系统 — 使用文档

## 快速开始

```go
import "ontology/internal/transfer"

svc := transfer.NewService(
    transfer.Config{
        TolerancePermille: 100, // 超收容忍千分比（10%），容忍额 = floor(发出量*100/1000)
        CloseWaitSeconds:  60,  // 未收齐时，自发出时刻起需等待 60 秒才能关闭
    },
    map[string]map[string]int64{
        "WH1": {"A": 100, "B": 10}, // 仓 → 商品 → 初始量
        "WH2": {"C": 5},
    },
)

_ = svc.Create("T1", "WH1", "WH2",
    []transfer.Line{{Item: "A", Qty: 40}, {Item: "B", Qty: 5}}, 0) // 冻结
_ = svc.Ship("T1", 1)                      // 发出：冻结 → 在途
_ = svc.Receive("T1", "A", 44, 2)          // 分批收货（含 4 的超收容忍）
_ = svc.Receive("T1", "B", 5, 3)
_ = svc.Close("T1", 4)                     // 全部收齐，可提前关闭

a, f := svc.Stock("WH2", "A")              // 查询：44, 0
lines, _ := svc.OrderLines("T1")           // 各行 发出/收货/短缺/盈余
svc.VerifyItem("A")                        // 守恒核验 → true
```

短缺场景的事后找回：

```go
_ = svc.Ship("T2", 10)
_ = svc.Receive("T2", "A", 30, 11)
_ = svc.Close("T2", 70)                    // 短缺 10
_ = svc.Recover("T2", "A", 6, 71)          // 找回 6，短缺变 4，目的仓 +6
```

## API 一览（`internal/transfer`）

| 方法 | 语义 |
| --- | --- |
| `NewService(cfg, initial)` | 初始化，登记每商品全网初始总量 |
| `Create(id, src, dst, lines, at)` | 创建并整单冻结（全有或全无） |
| `Cancel(id, at)` | 取消未发出单，释放冻结 |
| `Ship(id, at)` | 发出（仅一次），冻结扣源仓转在途 |
| `Receive(id, item, qty, at)` | 分批收货入目的仓；超 `issued+tolerance` 报超收 |
| `Close(id, at)` | 收齐随时可关；否则需 `at >= shipTime+wait` |
| `Recover(id, item, qty, at)` | 已关闭单短缺找回，入目的仓并减少短缺 |
| `Stock(wh, item)` | `(可用, 冻结)` 只读快照 |
| `OrderLines(id)` | 各行 `Requested/Issued/Received/Shortage/Overage` |
| `OrderStatus(id)` | `Created/Shipped/Closed/Cancelled` |
| `VerifyItem(item)` / `VerifyAll()` | 守恒自检 |

## 错误码与判定优先级

`*transfer.Error` 的 `Code` 字段：

1. `ErrInvalidArgument` 参数非法
2. `ErrClockRollback` 时钟回退（被拒操作不改状态/时钟）
3. `ErrTransferNotFound` 单据不存在
4. `ErrInvalidState` 状态不符（`Error.State` 给出当前状态）
5. `ErrInsufficientStock`（创建：错误信息含最小不足行下标）、`ErrOverReceipt`、
   `ErrCloseTooEarly`、`ErrRecoveryExceed`、`ErrNoShortage`

用 `transfer.IsCode(err, transfer.ErrOverReceipt)` 判定，`transfer.CodeOf(err)` 取码。

## 并发语义

- 所有方法可并发调用，结果等价于某个按全局时钟排序的串行执行。
- 查询在单元锁/单据锁下返回某个已完成操作之后的一致快照。
- 任何仓库任何商品的可用量、冻结量永不为负（核验与测试均强制检查）。
- 相同操作序列重放结果完全一致（无随机化、无 map 遍历依赖，
  核验遍历均先按 ID/仓库名排序）。

详见 `DESIGN.md`。
