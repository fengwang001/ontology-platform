# NOTES — chunked 增量快照导出

## 第三节推导：六键 a=1..f=6，chunkSize=2

| 步 | cursor 入参 | 返回块 | newCursor | done |
|---|---|---|---|---|
| `Next("")` | `""` | `a=1, b=2` | `b` | false |
| `Put(d,99)` | — | —（只改实时状态，快照不变） | — | — |
| `Next("b")` | `b` | `c=3, d=4` | `d` | false |
| `Next("d")` | `d` | `e=5, f=6` | `f` | **true**（位点后已无剩余键） |
| `Next("f")` | `f` | —（已完毕，返回 `ErrFinished`） | — | — |

- **(甲)** 若位点比较写成 `>=`：`Next("b")` 多导出键 `b`（块变成 `b=2,c=3`）；续传后 `b` 被导出两次（第一块和第二块各一次）。
- **(乙)** 若为活视图：`Next("b")` 读到实时 map，`d` 被导出成 **99**（正确应为快照时刻的 4）。
- **(丙)** 若用「本块条数 < chunkSize」判 done：第三块 `Next("d")` 满块（2=2），done 错成 **false**（正确为 true）；客户端必须再拉第四次 `Next("f")` 拿到空块才知结束。错在：末块恰好满块时与中间满块无法区分，完成信号晚一个往返，且把「已结束」与「还有数据」混为一谈——按规范 done 之后应得 `ErrFinished`，错误实现却让一次合法的空拉取成为必需。

## 四条不变量：保证位置 + 钉住它的测试

1. **与朴素参照一致**：`snap.NewSnapshot` 拷贝并排序、`export.Next` 依序切块的实现保证；测试 `TestChunkedConcatEqualsFull`。
2. **无重复无遗漏**：严格 `>` 定位（`snap.Locate`）+ newCursor 前进保证；测试 `TestNoDupNoGapOrdered`。
3. **点时刻一致**：`Snapshot` 时整体拷贝（`api.Snapshot` → `snap.NewSnapshot`），导出只读副本；测试 `TestPointInTime` 与 `TestConcurrentExportConsistent`。
4. **失败不留痕**：所有校验在写状态之前返回哨兵错误（`api.New/Put/Del/Next/Resume` 前置检查）；测试 `TestRejectedOpsLeaveState`、`TestErrorsDistinct`。
