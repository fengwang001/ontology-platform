# 部分列更新事件的批内合并（`merger` 包）

## 1. 列值三分

一个列在任意一条事件里严格处于以下三种互斥状态之一，由 `ColumnValue` 表达：

| 状态 | 构造 | `Present()` | `IsNull()` | 日志渲染 |
| --- | --- | --- | --- | --- |
| 缺席（列不在本次事件中） | `Absent()` | `false` | `false` | `<absent>` |
| 显式空值 | `Null()` | `true` | `true` | `<null>` |
| 字符串（含空串） | `String(s)` | `true` | `false` | 带引号，如 `""`、`"x"` |

注意：**空串 `""` 与显式空值 `<null>` 是两个不同的真实值**，缺席则表示“本事件根本不碰这一列”。

## 2. 事件形态

- 插入（`Insert`）：必须给出**全部**列；每列都必须是显式值（`Null` 或字符串）。
- 更新（`Update`）：`Columns` 只含本次变更列；`Before` 的键集合必须与 `Columns`
  完全一致，值为该列在本次变更之前应有的镜像。

## 3. 合并规则

对同一主键，在批内按出现顺序逐条推演（`keyState.effective` 为“逐条应用后”的当前值）：

1. **更新接更新**
   - 变更列取并集；
   - 同一列被多次更新时，合并值取**最后到达**的值；
   - 每列的变更前镜像取**该列在批内第一次被更新时**校验通过的镜像（不是最后一条）。
2. **插入接更新（含更新再插入之外的任意交错）**：只要该键在批内出现过插入，
   合并结果类型仍为**插入**，输出给出全部列（后续更新覆盖插入值），插入结果**不做**无变化剔除。
3. **无变化列剔除**（仅当合并结果为更新）：某列最终值与其**批前**原值三态相等时，
   从合并更新中剔除（含镜像一起剔除）。剔空后该键在本批不产生实际写入，
   输出中仍保留一条空更新记录以体现“该键出现过”，但 `CommitResult.Applied` 不计入，
   `Skipped[key] = true`。
4. **顺序**：每个主键在一批中至多输出一条，输出顺序等于该键在批内**首次出现**的顺序
   （`MergedEvent.Order` 从 0 递增）。

合并后的表状态与“逐条应用原始事件”完全一致；测试中以朴素参照 `naiveApply`
在 300 组随机场景（含三态值、交错键、合法与非法批次）下逐案比对最终状态与拒绝码。

## 4. 校验顺序与错误类别

校验在纯局部状态上进行，**整批要么全部成功，要么整体拒绝**；任何拒绝都发生在写表之前，
失败不留痕。错误由 `*BatchError` 表示，`Code` 互不相同、可程序化区分：

| Code | 触发条件 |
| --- | --- |
| `empty_key` | 主键为空串 |
| `unknown_event_type` | 类型既不是 `insert` 也不是 `update` |
| `unknown_column` | 变更列或镜像列不在表结构内 |
| `insert_missing_column` | 插入未给出全部列（`Column` 记录首个缺失列） |
| `empty_update` | 更新不含任何变更列 |
| `change_mirror_column_mismatch` | 变更列集合与镜像列集合不同 |
| `key_not_found` | 更新的键在批前表和批内插入中都不存在 |
| `key_already_exists` | 插入的键在批前表已存在，或本批已插入过 |
| `before_image_mismatch` | 镜像与实际不符：列首次被更新时对不上批内当前值；重复更新时对不上上一条事件的后到值 |

每条错误还带 `Index`（批内事件下标）、`Key`、`Column` 与可读 `Detail`。

## 5. 边界

- 空批：提交成功，输出为空，表不变。
- 一列在批内被改成别的值再改回原值：更新场景下被剔除（净效果为零）；插入场景下保留后到值。
- 插入事件中即使列值与某些默认想象相同，也不做剔除——插入必须落全部列。
- 表结构在 `NewTable` 时固定：列名不可为空、不可重复；空表结构只允许空批。

## 6. 并发模型

`Table` 内部使用 `sync.RWMutex`：

- `Commit` 取写锁，批次之间串行提交；
- `Get` / `Snapshot` / `SelfCheck` 取读锁，可被多个执行体并发调用，且可与提交并发
   （读操作永远看到某个完整提交后的一致快照）。

## 7. 日志

向 `NewTable` 传入非空 `io.Writer` 后，每批按 `[batch N]` 前缀打印：

- 每条事件的原始输入（类型、键、变更列、镜像列，值带三态渲染）；
- 每条更新镜像的判定依据（首次更新 / 重复更新、是否匹配）；
- 每个键的合并结果（插入或更新、保留/剔除了哪些列及原因）；
- 落表动作、跳过的空更新键；
- 拒绝批次的错误原因与“表未改变”的结论。

## 8. 本地验证

```bash
go test ./...
go test -race -v ./merger
go vet ./...
gofmt -l .
```

关键用例：

- `TestThreeWayValues`：缺席 / 显式空值 / 空串区分；
- `TestInsertThenUpdate`：插入接更新合并为一条插入；
- `TestUpdateUnionAndLastWins`：列并集、同列后到值、镜像取首次；
- `TestNoChangeDropping`：无变化列剔除与剔空跳过；
- `TestRejectionsLeaveNoTrace`：各类非法输入整批拒绝后状态不变；
- `TestDistinctErrorCodes`：九种错误码互不相同；
- `TestMergeMatchesNaive`：随机场景下与朴素参照逐条应用一致；
- `TestConcurrentReadsDuringCommit`：并发读 / 自检与提交竞争（`-race`）；
- `TestOutputOrderIsFirstAppearance`、`TestLogShowsInputsMergesAndDecisions`：顺序与日志。
