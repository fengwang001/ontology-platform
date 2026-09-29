# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 分组聚合视图增量维护（`ontology` 包）

`ontology.Maintainer` 随插入/删除实时维护一个带过滤条件的分组聚合视图，
并为每次成功的变更输出净变化日志，使下游严格按日志顺序回放即可得到正确视图。

### 过滤条件

通过 `ontology.Config` 配置（阈值均为“含等于”）：

- `MinCount >= 1`：组进入视图所需的最小行数；
- `MinSum`：组进入视图所需的最小求和；
- `MaxGroups`：非空组数量上限，`0` 表示不限制。

一个组出现在视图中，当且仅当 `count >= MinCount && sum >= MinSum`。
行数降为零时组从内部状态消失（不再占组数）；求和或行数跌破阈值时组离开视图。

### 输出规则（每条变更后比较该组前后是否在视图中）

| 情形 | 条件 | 输出顺序 |
| --- | --- | --- |
| enter | 前不在、后在 | `UPSERT(新值)` |
| leave | 前在、后不在（含行数归零） | `RETRACT(旧值)` |
| change | 前后都在 | `RETRACT(旧值)` 然后 `UPSERT(新值)` |
| — | 前后都不在 | 不输出 |

输出按组名排序、组内先撤回后写入，保证同一输入序列产生完全相同的输出；
下游对 `RETRACT` 删除该组、对 `UPSERT` 覆盖该组即可。

### 拒绝规则（整批原子，可区分原因）

被拒绝的批不会改变聚合、下游视图，也不会产生日志。错误均为
`*ontology.RejectError`，可用 `errors.Is` 区分：

- `ErrEmptyGroup`：插入行的组名为空（删除只按行 ID 定位，不要求组名）；
- `ErrEmptyRowID`：行 ID 为空；
- `ErrDuplicateInsert`：插入已存在的行 ID（含同批重复）；
- `ErrDeleteMissing`：删除不存在的行；
- `ErrTooManyGroups`：批提交后非空组数超过 `MaxGroups`。

同一批内“先删后插”同一行 ID 属于合法更新语义，不会被判为重复插入。

### 日志

- `ontology.TextLogger`：向任意 `io.Writer` 打印输入条目、每个组前后聚合、
  是否在视图中、判定依据（enter/leave/change/none）及输出条目；
- 也可实现 `ontology.Logger` 接口接收结构化的 `ontology.BatchLog`。

### 用法示例

```go
m, err := ontology.NewMaintainer(ontology.Config{
    MinCount:  2,
    MinSum:    10,
    MaxGroups: 100,
}, ontology.NewTextLogger(os.Stdout))
if err != nil {
    log.Fatal(err)
}

entries, batchLog, err := m.Apply([]ontology.Change{
    {Row: ontology.Row{ID: "a", Group: "g1", Value: 6}},
    {Row: ontology.Row{ID: "b", Group: "g1", Value: 6}}, // g1 恰好达到阈值 -> enter
})

entries, _, err = m.Apply([]ontology.Change{
    {Deleted: true, Row: ontology.Row{ID: "a"}},
    {Deleted: true, Row: ontology.Row{ID: "b"}}, // 行数归零 -> leave
})

view := m.Snapshot() // 并发安全，返回的每组都满足过滤条件
```

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 直接运行
go run ./cmd/server

# 编译后运行
go build -o bin/server ./cmd/server
./bin/server
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology
go test -race -run TestConcurrentReads ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
