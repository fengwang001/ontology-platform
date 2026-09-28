# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

当前提供 **支持分组键变更的增量分组聚合组件**（`ontology` 包）：随着行的
插入、更新与删除，实时维护每个分组的**求和**与**计数**，并输出可供下游
按序应用的**净变化日志**。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 直接运行（内置演示：插入 / 同组更新 / 改键 / 删除 / 非法批拒绝）
go run ./cmd/server

# 编译后运行
go build -o bin/server ./cmd/server
./bin/server
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出（测试日志含每条输入、输出条目与判定依据）
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestGroupKeyChange ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## 聚合规则

- 行表是 `rowID -> (group, value)`。每个分组维护 `sum`（组内所有行 `value`
  之和）与 `count`（组内行数）。
- **组只在 `count > 0` 时出现在视图中。** 特别地，**求和为 0 但计数为正的
  组必须保留**（例如 `+5` 与 `-5` 两行同组，和为 0、计数为 2）。计数归零的
  组立即从视图与组数统计中移除。
- 任意时刻的增量视图都与“对当前行表直接分组重算”的结果完全一致。

## 操作与分组键变更

每个操作先确定其**前、后分别影响哪个组**，再按下述顺序输出：

1. **插入**：向目标组写入新值（`sum += value, count += 1`）。
2. **更新**：
   - 先从**旧组撤回旧值**（`sum -= oldValue, count -= 1`）；
   - 再向**新组写入新值**（`sum += newValue, count += 1`）。
   - 当 `oldGroup != newGroup` 时即为**分组键变更**：保证**先输出旧组的
     撤回、再输出新组的加入**；旧组若因此清空则从视图消失，新组出现。
   - 同组更新时两条条目作用于同一组，净效果即 `value` 的变化，计数
     `−1/+1` 抵消。
3. **删除**：从行所在组撤回旧值（`sum -= value, count -= 1`）。

## 输出（净变化日志）

- 每条 `LogEntry` 携带：全局单调连续的 `Seq`、批次内下标 `OpIndex`、
  `RowID`、`Group`、条目类型（`RETRACT` 撤回 / `ADD` 写入）、应用到组上的
  带符号增量 `Value`/`Count`，以及应用后的 `SumAfter`/`CountAfter`。
- 下游**只需按 `Seq` 顺序把增量叠加到自己的分组视图、计数归零即移除组**，
  就能始终得到与本组件一致（也与批量重算一致）的结果，无需回放全部行。
- 除拉取 `Log()` 外，还可用 `Stream(ctx, ch)` 订阅：先完整回放历史日志，
  再接续实时日志，不重不漏；通道满时对写入形成背压，`ctx` 取消即停止。

## 非法输入与批次原子性

一批操作要么全部生效，要么整批拒绝（staging 副本 + 提交）。下列情况都会被
拒绝，且原因可通过 `errors.Is` 与返回的 `*RejectError`（携带批次内下标）
区分；**被拒绝的批不改变行表、聚合，也不产生任何日志**：

| 原因 | 触发条件 |
| --- | --- |
| `ErrDuplicateRow` | 插入已存在（含同批次内已插入）的行 ID |
| `ErrRowNotFound` | 更新或删除不存在的行 |
| `ErrEmptyRowKey` | 行 ID 为空 |
| `ErrEmptyGroupKey` | 插入/更新使用了空分组键（删除不校验该字段） |
| `ErrTooManyGroups` | 操作后非空组数超过 `New(maxGroups)` 的上限（`<=0` 不限） |
| `ErrInvalidOp` | 未知操作类型 |

组数按“当前活跃（计数为正）组数”计算：把某组最后一行改走或删除会释放名额，
并入已存在的组不新增组数。

## 并发模型

- 内部为单 goroutine actor，写入串行、读取（`Snapshot`/`Log`/`Rows`）与
  写入可并发调用，返回一致快照；`go test -race` 验证无数据竞争。
- 计算是确定性的：同一输入序列在任意实例上反复计算，得到逐条相同的日志与
  完全相同的最终视图。

## 主要 API

```go
agg := ontology.New(0) // 0 = 不限组数

entries, err := agg.Apply([]ontology.Op{
    {Kind: ontology.OpInsert, RowID: "a", Group: "g1", Value: 5},
    {Kind: ontology.OpUpdate, RowID: "a", Group: "g2", Value: 3}, // 改键
    {Kind: ontology.OpDelete, RowID: "a"},
})

view := agg.Snapshot() // []GroupView，按组名排序，仅含 count>0 的组
all  := agg.Log()      // 全部已提交的净变化日志
```
