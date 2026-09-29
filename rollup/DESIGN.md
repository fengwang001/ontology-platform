# 多级小计的增量维护与撤回（rollup）

`package rollup` 对带两个分组维度（Dim1、Dim2）的行流做三层分组的增量计数与求和，
产出带层级号、带符号的变更日志。下游按序重放日志得到的结果，与对当前行集直接
批量重算完全一致且可复现。

## 三层分组

| 层级号 | 名称 | 分组键 | 内容 |
| --- | --- | --- | --- |
| 1 | 明细组 `LayerDetail` | `(Dim1, Dim2)` | 每个维度组合的计数与求和 |
| 2 | 第一维小计 `LayerSubtotal` | `(Dim1)` | 同一 Dim1 下全部明细组之和 |
| 3 | 总计 `LayerGrand` | 汇总占位键 | 全部行之和 |

恒等关系（任意时刻成立，包括并发期间与任意日志前缀重放结果）：

- 小计 = 其 Dim1 下所有明细组的 `(count, sum)` 逐项相加；
- 总计 = 所有明细组之和 = 所有小计之和 = 行集大小与数值之和。

## 增量与变更日志

一条增量是 `Increment{Op, Row}`，`Op` 为 `OpAdd`（新增）或 `OpRemove`（撤回）。
每条被接受的增量在同一临界区内按 **L1 → L2 → L3** 顺序更新三层，并产生
一条 `LogEntry`，其中固定包含 3 条 `Change`：

- `CountDelta` / `SumDelta` 为带符号增量：新增为正，撤回为负；
  撤回的 `SumDelta` 取被删行加入时数值的相反数（精确抵消）。
- `ResultCount` / `ResultSum` 为应用该变更后组上的结果值，便于下游无状态重放。
- 某层组计数归零时，该层只输出这一条撤回（`CountDelta=-1`、结果计数 0），
  组随即从该层删除；**计数大于零但求和为零（含负数相抵、value=0）的组必须保留**。
  总计层永不删除（归零即回到空总计）。

日志按提交顺序连续编号（`Seq` 从 1 开始）。一条 entry 只有在校验通过后才会被
追加并对外可见，因此：

- 任意**完整 entry 前缀**重放后三层恒等式都成立（逐前缀自洽）；
- 全量重放结果与当前状态、与 `BatchRecompute(rows)` 逐层相等。

下游用法：`NewReplay()` 后对 `Store.Log()` 逐条 `ApplyEntry`；
也可用 `BatchRecompute(rows)` 独立批量重算做对账。

## 空值语义

- 维度字段为 `*string`：`nil` 表示空值 NULL，是**真实的分组取值**。
  `(NULL, y)`、`(A, NULL)`、`(NULL, NULL)` 各自独立成组。
- 汇总占位由键的 `HasDim1/HasDim2=false` 且 `Layer=LayerGrand` 表示，
  与某一维恰好为 NULL 的明细/小计组在键结构上严格不同，永不会混淆。

## 输入约束与错误类别

非法输入被**整体拒绝**：在校验阶段返回互不相同的哨兵错误，任何已存在状态
（行集、三层、日志）都不改变，失败不留痕。可用 `errors.Is` 区分：

| 错误 | 触发条件 |
| --- | --- |
| `ErrInvalidIncrement` | `Op` 非 Add/Remove，或行标识 `ID` 为空 |
| `ErrDuplicateRow` | 新增的 `ID` 在当前行集中已存在 |
| `ErrRowNotFound` | 撤回一条当前不存在的行（删除必须命中真实存在的行） |
| `ErrTooManyGroups` | 新增会使明细组数超过 `New(maxGroups)` 的上限 |

边界说明：

- 明细组上限只统计“不同 `(Dim1, Dim2)` 键”的数量；新增行落入已存在明细组
  不增加组数，不受上限影响。`maxGroups<=0` 使用 `DefaultMaxDetailGroups`。
- 撤回只需提供行 `ID`；分组键与数值以存储中的原行为准，保证负变更精确抵消。
- `Value` 为 `int64`，可为负或零；本层不做数值合法性限制。

## 并发模型

`Store` 内部使用单一 `sync.RWMutex` 串行化提交，并以读锁支持多个执行体并发调用
`DetailGroups` / `Subtotals` / `Total` / `Rows` / `Log` / `Verify`。
提交是“先完整校验、后一次应用”，因此被拒增量在锁内即返回，读者不可能观察到
中间态或被拒痕迹。

`Verify()` 校验：组计数为正、明细→小计→总计恒等式、行集大小等于总计计数，
并对日志逐前缀重放做自洽检查、对全量日志做最终一致性检查。

## 本地验证

```bash
# 全量测试（测试日志会逐步打印输入增量、输出的三层变更与判定依据）
go test -v ./rollup

# 竞态检测 + 反复执行（含并发提交/查询/自检）
go test -race -count=3 ./rollup

# 全仓库、覆盖率、静态检查、格式
go test -race ./...
go test -coverprofile=cov.out ./... && go tool cover -func=cov.out
go vet ./...
gofmt -l .
```

注：若环境默认 `GOCACHE` 只读，可指向可写目录，例如 `GOCACHE=/tmp/gocache`。
