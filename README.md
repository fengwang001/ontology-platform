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

## 多分区合并水位线（`watermark` 包）

`watermark.Merger` 合并多个分区上报的事件时间水位线，保证长时间无数据的
空闲分区不会卡死整体水位。

### 核心概念与规则

- **处理时间由调用方注入**：`Report` / `AdvanceClock` 都携带 `now`，组件
  不读墙钟。`now` 只进不退，回退直接拒绝（`ErrClockBackward`）。
- **分区状态**：每个分区维护「分区水位」与「最后活跃时间」。仅 `Report`
  会刷新该分区的最后活跃时间；`AdvanceClock` 只推进全局处理时间，用于
  驱动空闲判定。
- **空闲判定（边界恰好相等即空闲）**：对已上报过的分区，当
  `当前处理时间 - 最后活跃时间 >= idleThreshold` 时视为空闲；差 1 不算。
  从未上报过的分区没有水位，天然不参与计算。
- **候选值**：候选集 = 所有「已上报且非空闲」的分区，候选值取其中分区
  水位的**最小值**。
- **合并水位推进规则**：
  - 候选集非空：`合并水位 = max(当前合并水位, 候选最小值)`；
  - 候选集为空（全部空闲，或尚无任何上报）：合并水位**保持不变**；
  - 因此合并水位单调不减。空闲分区带着低值恢复时，其值只参与取小，
    **不会把合并水位拉退**；它需要先追上当前合并水位，才能继续推动整体前进。
- **拒绝即无副作用**：分区越界（`ErrPartitionOutOfRange`）、时钟回退
  （`ErrClockBackward`）、分区水位回退（`ErrWatermarkBackward`）、构造参数
  非法（`ErrInvalidArgument`）都会被拒绝，且不改变分区水位、活跃时间、
  时钟或合并水位。错误均包装了对应的哨兵错误，用 `errors.Is` 区分原因。
  时间相等、水位相等属于「不退」，允许提交。

### 用法示例

```go
import "ontology/watermark"

// 3 个分区，处理时间相差 10 个刻度无数据即视为空闲
m, err := watermark.New(3, 10)

_ = m.Report(0, 100, 0) // p0 在 t=0 上报水位 100
_ = m.Report(1, 50, 0)  // p1 上报 50，候选最小 50，但合并水位只进不退 → 100
_ = m.AdvanceClock(10)  // t=10：p0 恰好达到空闲边界被剔除，p1 仍活跃
_ = m.Watermark()       // 读取合并水位，可并发调用，结果单调不减
```

每次成功提交都会记录一条结构化日志（`slog`），包含输入
（`partition` / `input_watermark` / `now`）、判定依据（活跃 / 空闲 /
未上报分区列表、候选值是否存在及 `candidate_min`）、推进前后的合并水位；
被拒绝的操作以 WARN 记录具体 `reason`。可用 `Snapshot()` 取得与日志一致的
全量状态。

### 本地验证

```bash
# 全量测试（含竞态检测）
go test -race -v ./watermark/

# 只跑某类场景
go test -run 'TestIdleBoundaryExactlyEqual|TestAllIdleKeeps' ./watermark/
go test -run TestConcurrentReadersObserveMonotonicWatermark ./watermark/

# 查看决策日志（-v 时输出每次输入、候选值与合并水位的判定过程）
go test -v -run TestMergeAdvanceIdleAndRecover ./watermark/

# 覆盖率
go test -coverprofile=coverage.out ./watermark/
go tool cover -html=coverage.out
```

测试覆盖：空闲边界恰好相等、空闲分区以低于合并水位的值恢复、全部空闲保持
不变、各类非法输入被拒绝且无状态变化、同一输入序列重放的确定性（逐步快照
与去时间戳日志逐字节一致）、并发读写下合并水位单调不减（`-race`）。

