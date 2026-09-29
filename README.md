# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

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

## 按事件时间取最新（LWW）物化视图

`ontology` 包提供事件时间乱序下的最后写入胜出键值视图（`ontology/view.go`），
通过 `NewMaterializedView(maxKeys)` 创建，`Apply([]Event)` 原子地写入一批事件。

### 仲裁规则

- 每个键维护一条“当前已生效记录”的事件时间水位，**删除也会推进并保留水位**。
- 事件时间**严格大于**水位时生效；**不大于（迟到或相等）一律忽略**并计入 `DroppedCount`，值与水位都不变。
- 事件时间相等时**先到者胜**：批内按到达顺序、跨批按应用顺序，后到的写入/删除全部忽略。
- 生效时若键当前存在旧值，变更日志先输出 `RETRACT`（撤回旧值），再输出：
  - `ESTABLISH`：写入新值（`Value` 非 nil，**空字符串也是合法值，表示“存在且值为空”**）；
  - `TOMBSTONE`：删除生效（`Value == nil`，标记“不存在”，但水位保留）。
- “值为空字符串的存在”与“不存在（墓碑）”是两种不同状态：前者 `Get` 返回值与 `ok=true`，
  后者返回 `ok=false`，但更小事件时间的写入都无法复活该键。

### 批次原子性与拒绝原因

一批事件中只要有一条非法，整批不生效、状态与丢弃计数均不变，错误可通过 `errors.Is` 区分：

- `ontology.ErrInvalidArgument`：批次为 nil、事件时间为负、`maxKeys <= 0`；
- `ontology.ErrEmptyKey`：存在空键；
- `ontology.ErrTooManyKeys`：按事件时间模拟整批后，存活键数会超过上限。

空批次（非 nil、长度 0）是合法的 no-op。

### 并发读取

每次 `Apply` 完成后原子发布一个不可变快照，`Get`、`Snapshot`、`View`、`DroppedCount`、
`Changes`、`SelfCheck` 均可与写入并发调用。单个快照内的键值、丢弃数、变更条数逐字段来自同一版本；
停顿（无写入）时并发调用返回完全相同的结果。

### 本地验证方法：批量按事件时间核对

乱序重放后，最终每个键的正确结果可以不依赖流式实现直接算出：

1. 把**全部已接受批次**按应用顺序展开成事件流；
2. 对每个键只保留事件时间最大的事件；事件时间相同则保留出现最早的一条（先到者胜）；
3. 该事件 `Value == nil` 则键最终不存在，否则键存在且值为该事件的值、事件时间为其时间。

测试 `TestBatchEventTimeOracle` 用固定随机种子生成 200 批乱序事件（含删除、空值、
同时间冲突与超限拒绝），同时用独立的流式参考模型核对 `AppliedEvents/DroppedEvents`，
再用上述全局 argmax 复核 `Snapshot()`，并用同一批次序列重放第二个实例验证可复现。

```bash
# 乱序仲裁、同时间仲裁、空值/墓碑、整批拒绝、并发读写（带竞态检测）
go test -race -v ./ontology

# 只看批量按事件时间核对的可复现性用例
go test -race -v ./ontology -run TestBatchEventTimeOracle
```

单测日志（`-v` 可见）会逐批打印事件、撤回/建立变更日志、判定依据以及最终键值与水位，
便于人工按事件时间核对每一步为什么生效或被判迟到。
