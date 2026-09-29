# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 事件时间最后写入胜出物化视图（`lwwview`）

`lwwview` 包在事件乱序到达时，按**事件时间**而不是到达顺序保留每个键的最新记录，
并输出“撤回（Retract）/建立（Upsert）”的可复现变更日志。

### 仲裁规则

- 每个键维护当前物化记录的事件时间；写入或删除的事件时间**严格大于**当前事件时间才生效。
- 事件时间不大于当前值（即更早或相等）的事件一律视为**迟到忽略**，计入 `Dropped()`，不改变任何键的值与事件时间。
- **事件时间相等时先到者胜**：同一事件时间只有第一条事件生效，后续事件（无论是写入还是删除）都被忽略；跨批次同样成立。
- 生效时若键当前已有记录，先输出一条 `KindRetract` 撤回旧状态，再输出一条 `KindUpsert` 建立新状态；新键只输出 Upsert。
- 删除（`Value == nil`）使键**不存在**，其事件时间作为墓碑保留，继续用于仲裁之后的迟到事件；查询时该键表现为不存在。
- 空值写入（`Value` 指向 `""`）使键**存在且值为空**，与“不存在”是两种可区分的状态：
  `Lookup` 对空值键返回 `ok=true, Exists=true, Value=""`，对已删除或从未出现的键返回 `ok=false`。
- 一批事件先在影子状态上整体校验与模拟，任一条非法（空批次等非法参数、空键、不同键数超过容量）都会**整批拒绝**，状态与变更日志完全不变；
  拒绝原因可通过 `errors.Is` 区分为 `ErrInvalidArgument` / `ErrEmptyKey` / `ErrTooManyKeys`。
- 所有读写由内部读写锁保护；`SnapshotWithDropped()` 在同一把读锁下返回逐字段一致的快照与丢弃计数，可安全并发调用。

### 本地验证方法：批量按事件时间核对结果

验证思路是**独立重算（oracle）**：`lwwview.ExpectedByEventTime` 不依赖视图内部实现，
只按“事件时间取最大、相等取先到”的规则重算每键获胜记录；`lwwview.VerifyFreshBatch`
把一批事件灌进全新视图后，与 oracle 逐键核对值、事件时间、存在性，并通过 `SelfCheck()`
重放全部变更日志核对最终状态。

```bash
# 内置乱序样例（含相等仲裁、迟到删除、空值、墓碑）
go run ./cmd/lwwverify

# 使用 JSON Lines 事件文件核对：
# 每行 {"key":"...","event_time":123,"value":"..."}
# value 省略或为 null 表示删除；value:"" 表示空值写入
go run ./cmd/lwwverify -file cmd/lwwverify/sample-events.jsonl -max-keys 16
```

代码中可直接调用：

```go
view, expected, err := lwwview.VerifyFreshBatch(maxKeys, events)
fmt.Println(expected) // 每键获胜事件下标与最终 Entry，便于核对判定依据
fmt.Println(view.SelfCheck())
```

单测会打印每个场景的输入事件、变更日志、键值与判定依据：

```bash
go test ./lwwview -v -run TestEqualEventTimeFirstWins
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
