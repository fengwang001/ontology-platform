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

## 一致性读取器（`consistency` 包）

带滞后上限的一致性读取器：按最大滞后读取提交位点附近的快照，支持降级与阻塞两种模式，读到的位点满足滞后约束且始终可复现。

### 位点推进

- 读取器维护两个单调推进的位点：提交位点 `commit` 与已应用位点 `applied`，不变量为 `applied <= commit`（由 `Check()` 自检）。
- `AdvanceCommit(to)`：提交必须连续，`to` 必须恰好等于 `commit + 1`，否则整体拒绝并返回 `ErrNonContiguousCommit`。
- `MarkApplied(to)`：`to` 必须大于当前 `applied` 且不超过 `commit`，否则整体拒绝并返回 `ErrAppliedOutOfRange`；推进成功后唤醒全部阻塞中的等待者。
- `Read(maxLag, mode)` 的 `maxLag` 为负时整体拒绝并返回 `ErrNegativeLag`。
- 任何一次失败都不会改变两个位点与等待者状态。

### 读取语义

两种模式都在**调用时刻**冻结目标位点 `target = max(commit - maxLag, 0)`：

- **降级模式（`ModeDegraded`）**：立即返回当前已应用位点，并通过 `Result.Degraded` 精确报告是否降级——`applied < target` 时为 `true`（`applied == target` 恰好满足约束，不降级）。
- **阻塞模式（`ModeBlocking`）**：若 `applied` 未达 `target` 则阻塞，直到 `applied` 推进到位后返回；此后 `commit` 再推进也不改变本次已冻结的 `target`，因此本次读到的位点始终可复现。`maxLag = 0` 时 `target == commit`，即强一致读。

`Snapshot()` 与 `Check()` 可并发调用；快照在同一把锁内读取，并发读到的位点逐字段相同；`MarkApplied` 通过广播并发安全地放行多个等待者。

### 本地验证（朴素参照核对）

`consistency/reader_test.go` 中的 `TestAgainstNaiveReference` 内置一个朴素参照实现（`naive` 类型，用显而易见的简单逻辑维护位点并回答读取），对 2000 步随机操作序列（合法/非法提交、越界/回退应用、负滞后读取）逐步比对两个位点、返回位点、目标位点与降级判定。本地运行：

```bash
# 跑全部测试（含竞态检测与详细日志，日志含操作、两个位点、返回位点与判定依据）
go test -race -v ./consistency/

# 只跑朴素参照核对
go test -race -v -run TestAgainstNaiveReference ./consistency/
```
