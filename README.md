# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

当前组件：**撤回式变更流上的分组增量去重计数器**（`ontology` 包）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 模型与规则

### 多重性（multiplicity）

对每个 `(组, 值)` 维护净多重性：

```
mult(g, v) = 插入次数 - 撤回次数
```

- 插入（`KindInsert = +1`）使多重性 +1，撤回（`KindRetract = -1`）使多重性 -1。
- 组的**去重计数** = 该组内多重性**严格为正**的值的个数。
- 多重性归零的 `(组, 值)` 与去重计数归零的组都会从内部状态中删除，不出现在视图里。

### 批与折叠（fold）

`Apply([]Change)` 以**批**为单位原子执行，按条目顺序处理：

1. 先检查整批条目数是否超过 `NewDedupCounter(maxEntries)` 设定的上限。
2. 逐条校验空组名、空值、非法符号。
3. 每条撤回在「批前已提交状态 + 批内此前各条已生效变更」的**临时状态**上校验：
   撤回一个当前多重性为零的值 → 拒绝整批。
4. 整批合法后才一次性折叠提交，并按组名升序输出每个被触及组的
   `GroupDelta{Group, Before, After, Delta}`。

批内"先插入再撤回同一值"净变化为 0，计数不变；颠倒为"先撤回再插入"
则首条撤回在零多重性上执行，整批拒绝。

### 拒绝原因（可区分）

| Reason | 含义 | EntryIndex |
|---|---|---|
| `too_many_entries` | 条目数超过上限 | -1 |
| `empty_group` | 组名为空 | 首个违法条目下标 |
| `empty_value` | 值为空 | 首个违法条目下标 |
| `invalid_kind` | 符号既非插入也非撤回 | 首个违法条目下标 |
| `retract_zero` | 撤回当前多重性为零的值 | 首个违法条目下标 |

被拒绝的批**不会**改变多重性、计数视图，也不会修改此前已产生的任何日志；
仅在日志末尾追加一条拒绝记录。

### 输出与日志

- `ApplyResult.Changes` 给出各组去重计数的净变化；下游按序号顺序把
  `Delta` 累加进各组建账，即可始终复现正确的去重计数（拒绝条目不产生 Delta）。
- `DedupService` 把"应用状态 → 读批后视图 → 追加日志"串行化为一个原子步骤，
  每条 `LogEntry` 完整记录输入批次、判定依据（接受/拒绝原因与位置）、
  输出净变化与批后视图。
- 日志不注入墙钟时间，序号按追加顺序分配，因此**同一输入序列反复计算
  得到完全相同的输出与日志**。

### 并发语义

- `Apply` 可被多 goroutine 并发调用；状态、视图、日志的更新互斥且原子。
- `View()` / `Multiplicity()` / `Journal.Entries()` 返回持锁期间制作的
  **深拷贝快照**，并发读取得到的视图逐字段一致，且调用方修改快照不影响内部状态。

## 运行

```bash
# 拉取依赖
go mod tidy

# 直接运行演示：多批变更的输入/判定/输出/批后视图
go run ./cmd/server

# 编译后运行
go build -o bin/server ./cmd/server
./bin/server
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出（测试日志中打印每批输入、输出条目与判定依据）
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestRetract ./ontology -v

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
