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

## 跳洞检测器（`gap` 包）

`gap.Detector` 按序号连续性检测确认丢失的序号（跳洞），可容忍轻微乱序，
且重复段不会误报。实现位于 `gap/detector.go`，全部规则由
`gap/detector_test.go` 锁定，可用 `go test -race -v ./gap` 复现。

### 状态定义

检测器只维护三部分状态：

- `water`：已确认连续前缀上界（水位）。所有 `<= water` 的序号均已有定论，
  水位单调不减。
- `inflight`：已到达但相对当前水位超前的在途序号集合（去重）。
- `holes`：已确认丢失的洞序号集合。一旦确认，永不回撤。

### 摄入与精确判洞规则

`Ingest(seq)` 的处理顺序：

1. `seq <= water`：属于已确认前缀（无论它曾是正常序号还是洞），按重复段
   整体忽略，不改变任何状态，也绝不产生新判定。
2. 否则把 `seq` 放入 `inflight`，然后反复收敛直到在途集为空，
   或最远在途序号与水位之差不超过窗口：
   - 令 `next = water + 1`（下一个期望序号）；
   - 若 `next` 已在 `inflight`：立即并入连续前缀，`water = next`；
   - 否则令 `M = max(inflight)`：
     - `M <= next + window`：`next` 可能只是乱序迟到，继续等待，不做判定；
     - `M > next + window`：`next` 在乱序窗口内到达的可能性已经不存在，
       确认为洞（加入 `holes`，不可回撤），`water = next`。

注意“已到达先并入、缺失才判洞”的顺序不可颠倒：即使更前方的缺口已越窗，
已到达的 `next` 也必须先并入，否则会把已到达序号误判为丢失。

边界为严格不等号：`M == next + window` 时等待，`M == next + window + 1`
时才判洞。`window = 0` 表示严格连续，任何超前到达都会让当前缺口立即判洞。

迟到但最终到达的序号，若到达时 `<= water`，按重复段忽略；若它此前已被判洞，
洞集保持不变（不回撤）。

### 非法输入（整体拒绝、零副作用）

以下输入返回可区分的哨兵错误，且一次失败不会改变水位、在途集与洞集：

- `seq < 1`：`gap.ErrIllegalSequence`
- `seq > math.MaxInt64 - 1`：`gap.ErrSequenceOverflow`（预留余量，
  保证窗口推进与内部 `+1` 不溢出）
- `New(window)` 且 `window < 0`：`gap.ErrIllegalWindow`

### 并发语义

- `Ingest` 之间、`Water`/`Inflight`/`Holes`/`Check` 之间通过读写锁互斥，
  查询与自检可与摄入并发执行。
- 并发喂入互不相同的序号后，若实际乱序跨度不超过窗口，水位必然正确、
  洞集为空、在途集为空。乱序跨度的上界即窗口：若希望“全集最终到达就绝
  不判洞”，窗口需不小于首末到达序号的最大差值（参见
  `TestConcurrentDistinctSequences`）。

### 本地验证：全集对照法

`TestFullSetCrossCheck` 用一份独立的“到达全集”和一个用不同数据结构
（map + 每轮重算）实现的预言机，按与生产代码相同的规则逐点模拟：

- 40 轮小规模（300 序号、随机窗口 0–9、随机 50% 到达、随机顺序）：
  每摄入一个序号都对照水位、在途集、洞集三者完全一致；
- 1 轮大规模（4000 序号）：收敛后做一次全集逐元素对照。

本地可按以下方式核对结果：

```bash
# 普通全量
go test ./...

# 竞态 + 详细日志（打印每次摄入的序号、水位、在途集、洞集与判定依据）
go test -race -v ./gap

# 多次重复，确认判定可复现
go test -race -count=10 ./gap

# 只跑全集对照 / 并发用例
go test -race -run 'TestFullSetCrossCheck|TestConcurrentDistinctSequences' -v ./gap
```

单测日志格式固定包含：`seq`（本次序号）、`water`（水位）、
`inflight`（在途集）、`holes`（洞集）以及“连续并入 / 窗口内等待 /
越窗判洞不回撤 / 重复段忽略”的判定依据。
