# canfault — CAN 控制器故障界定状态机

包 `canfault` 实现一个可并发调用的 CAN 2.0 故障界定（fault confinement）状态机，
按发送/接收事件维护发送错误计数 `TEC` 与接收错误计数 `REC`，并在
**主动错误（ErrorActive）**、**被动错误（ErrorPassive）**、**总线关闭（BusOff）**
三态间迁移，含总线关闭后的恢复流程。

所有方法（`Apply` / `Restart` / `Idle11` / `Snapshot`）均在互斥锁保护下执行，
并发调用的结果等价于某一种串行交织顺序。

## 事件对计数的影响

`Apply(ev)` 一律按**事件发生前**的状态处理：

| 事件 | 处理规则 |
| --- | --- |
| `TxOK` | `TEC > 0` 时 `TEC -= 1`；为 0 时不下溢 |
| `TxErr` | `TEC += 8` |
| `TxAckErr` | 事件前为主动错误态：`TEC += 8`；事件前为被动错误态：两个计数都不变 |
| `RxOK` | `REC > 127` 时把 `REC` 置为 `127`；否则 `REC > 0` 时 `REC -= 1`（为 0 不下溢） |
| `RxErr` | `REC += 1` |
| `RxErrDominant` | `REC += 8`（错误标志期间检测到显地位） |

计数本身**不设上限**；仅状态推导对其分级。

## 三态导出规则

状态完全由当前计数导出（先判总线关闭）：

- `TEC > 255` → `BusOff`
- 否则 `TEC > 127` 或 `REC > 127` → `ErrorPassive`
- 否则 → `ErrorActive`

因此：非总线关闭时 `TEC <= 255`；总线关闭期间 `TEC > 255`，直到恢复结束计数清零。

## 总线关闭与恢复

- 总线关闭期间 `Apply` 一律被拒绝（`ErrApplyWhileBusOff`），计数保持不变。
- `Restart()`：仅在**总线关闭且尚未开始恢复**时有效——开始恢复并把恢复计数置 0。
  此操作本身不改变状态（仍为 BusOff），因此不产生迁移记录。
- `Idle11()`：仅在恢复中有效，表示观察到一个连续 11 个隐性位的序列，恢复计数加 1。
  - 第 1～127 次：仍在恢复中，保持总线关闭。
  - 第 128 次：`TEC` 与 `REC` 同时清零，状态回到主动错误，恢复结束（恢复计数归零）。

## 迁移记录与成功操作序号

- 成功操作含 `Apply`、`Restart`、`Idle11`，成功操作序号从 1 起单调递增。
- 仅当一次成功操作引起状态改变时，追加一条迁移记录
  `Transition{OpSeq, Old, New}`；相邻两条记录的 `New`/`Old` 首尾衔接。
- `Snapshot()` 返回 `TEC`、`REC`、`State`、`Recovering`、`IdleCount`、`OpSeq`
  与迁移记录的拷贝，外部修改不影响内部状态。

## 拒绝原因（可区分，`errors.Is` 判定）

| 场景 | 错误 |
| --- | --- |
| `Apply` 事件非法（不在六种事件内） | `ErrInvalidEvent` |
| 总线关闭时 `Apply` | `ErrApplyWhileBusOff` |
| `Restart` 时不在总线关闭 | `ErrRestartNotBusOff` |
| `Restart` 时已在恢复中 | `ErrRestartAlreadyRecovering` |
| `Idle11` 时不在恢复中 | `ErrIdle11NotRecovering` |

判定顺序：`Apply` **先**判事件非法**再**判总线关闭；`Restart` **先**判不在总线关闭
**再**判已在恢复中。任何被拒绝的操作都不会改变计数、恢复计数、迁移记录，也不占用
成功操作序号。

## 本地验证

在仓库根目录执行（若 `go` 不在 PATH，先 `export PATH=$PATH:/usr/local/go/bin`；
若默认构建缓存不可写，可加 `GOCACHE=/tmp/gocache`）：

```bash
# 全部用例（日志中逐步打印输入、输出与判定依据）
go test -v ./canfault

# 竞态检测 + 重复执行
go test -race -count=3 ./canfault

# 覆盖率
go test -cover ./canfault

# 全仓库
go test ./...
gofmt -l .
go vet ./...
```

测试要点：

- TEC=127（主动）与 TEC=128（被动）的状态差异；
- TEC 247→255 仍被动、248→256 进入总线关闭；
- REC=130 时 `RxOK` 置 127 并回到主动（TEC 不受影响）；REC=127 时 `RxErr`→128 进入被动；
- `TxAckErr` 主动态 +8、被动态不变；`TxOK` 在 TEC=0 时不下溢；
- 恢复第 127 次 `Idle11` 尚未完成、第 128 次完成并清零；恢复中再次 `Restart` 被拒；
- 各类被拒操作均不占用成功序号、不改变任何状态；
- `TestNaiveCrossCheck` 将一条 211 步混合脚本（含全部拒绝路径）与独立的逐步朴素模拟器
  逐字段对照；`TestDeterministicReplay` 验证同一脚本多次重放结果完全一致；
  `TestConcurrentOps` 在并发读写下校验计数与状态不变量（建议配合 `-race`）。
