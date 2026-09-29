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

## 有界键集合（ontology.BoundedSet）

`ontology/bounded_set.go` 提供按键最近活跃度有界蓄存的并发安全集合。

### 驱逐排序键与容量规则

- 每个键维护：最近活动时间戳 `Timestamp`，以及首次进入时分配、驱逐后**不复用**的位点 `Slot`（单调递增）。
- 驱逐排序键为二元组 `(Timestamp, Slot)`：超出容量时驱逐当前集合中活动时间戳最小者；时间戳并列时驱逐位点较小者。因此驱逐结果确定且可复现。
- 重复 `Track` 已存在的键只刷新活动时间戳（允许回退为更小值），集合大小不变；跟踪已被驱逐的键按新键处理，分配新的不复用位点。
- 集合大小恒等于 `Count()` 且永不超过容量；容量 `<= 0` 时构造直接失败（`ErrInvalidCapacity`）。
- 空键（`ErrEmptyKey`）与负时间戳（`ErrNegativeTimestamp`）整体拒绝，三种原因可用 `errors.Is` 区分；任何一次失败都不会改变集合、时间戳、位点分配与计数。
- `Track` / `Count` / `Snapshot` / `SelfCheck` 均可并发调用；`Snapshot` 在锁内一次性拷贝并按 `(Timestamp, Slot)` 升序返回，并发读不会看到内部中间态。

### 本地验证（朴素映射对照）

测试内置一个用朴素 `map` 实现的独立对照模型 `naiveModel`，用固定种子的随机操作序列（含大量时间戳并列与回退）逐步比对 `BoundedSet` 的计数与快照：

```bash
# 运行对照测试与全部单测（日志打印事件、各键状态、是否驱逐与判定依据）
go test -v -run TestAgainstNaiveModel ./ontology
go test -race -v ./ontology
```
