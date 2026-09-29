# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## aggview：带过滤条件的分组聚合增量视图

`aggview` 包在行不断插入/删除时增量维护一个带过滤（HAVING）条件的分组聚合视图，并为下游输出可顺序应用的净变化日志。

### 过滤条件

每个组维护两个聚合：行数 `count` 与求和 `sum`。组出现在视图中当且仅当（阈值均含等于）：

```
count >= MinCount  且  sum >= MinSum
```

组的行数降为零时，该组从内部跟踪状态中彻底删除（组消失）。

### 输出规则（净变化）

每条行变更后，比较该组变更前后的可见性：

| 前 \ 后 | 可见 | 不可见 |
| --- | --- | --- |
| 不可见 | 不输出 | 不输出 |
| 可见 | `change`：先 `retract(旧值)` 再 `upsert(新值)` | `leave`：`retract(旧值)` |

不可见 → 可见为 `enter`：仅输出 `upsert(新值)`；前后都不可见则不产生任何条目。日志条目带单调连续的 `Seq`，下游（见 `MaterializedView`）按顺序应用日志即可始终得到与 `View.Snapshot()` 一致的正确视图。

### 拒绝规则（原子批处理）

`Apply` 先对整批做基于状态模拟的校验，任一非法都会拒绝整批，聚合、下游视图与已产生的净变化日志均不改变。可区分的原因（`errors.Is`）：

- `ErrEmptyGroup`：插入的行组名为空。
- `ErrRowNotFound`：删除不存在的行（含同一批中已删除的行）。
- `ErrGroupLimit`：该批会使被跟踪组数超过 `MaxGroups`（0 表示不限）；即使新组在同批稍后又被删除也拒绝。
- `ErrUnknownOp`：不支持的变更操作。

### 并发与确定性

- `View` 内部用读写锁保护：`Snapshot()`/`Log()` 可被并发调用；并发读者看到的每个组都满足过滤条件。
- 写操作串行化，组按输入批内顺序逐条求值；同一输入序列在空视图上重放产生完全相同的日志与视图（已由测试验证）。
- 配置 `Logger` 后会打印每条 `input`（输入）、`decision`（前后聚合、过滤条件、判定依据）、`output`（输出条目）以及被拒绝批的 `reject batch ... reason=...`。

### 本地验证

```bash
go test ./aggview                 # 功能测试
go test -race -v ./aggview        # 含并发竞态检测与详细日志
go test -cover ./aggview          # 覆盖率
go vet ./... && gofmt -l .
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

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
