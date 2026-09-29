# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 全局时间戳分配器（`tso` 包）

主节点切换安全的混合逻辑时钟时间戳分配器，入口见 `tso/allocator.go`。

### 时间戳构成

- 时间戳为 `(Physical, Logical)`：物理毫秒 + 逻辑计数，按字典序比较
  （先比物理，再比逻辑），见 `tso/types.go`。
- 逻辑计数取值 `0 .. L-1`（`L` 为每毫秒容量）。
- 每次发放前，物理部分取 `max(本地时钟, 上次物理部分)`，因此时钟回拨
  不会产生更小的值；逻辑计数用尽后物理部分加一、逻辑清零。
- 一次请求的 `n` 个时间戳必须落在同一毫秒且逻辑连续；本毫秒剩余不足
  `n` 时整批移到下一毫秒（而不是跨毫秒拆分）。

### 持久化上界与续写

- 共享存储保存 `(Term, High)`，`High` 是物理毫秒的**排他上界**：
  任何已发出时间戳的物理部分必须严格满足 `Physical < High`。
- 存储只接受 `任期 >= 存量任期` 的原子写入（`Store.CompareBound`
  在单把互斥锁内完成读后写）。
- 发放时若剩余窗口 `High - Physical` 不足 `W` 的一半
  （`2*remaining < W`），先以 `max(Physical, High) + W` 作为新上界
  原子续写，再发放。
- 续写通过故障注入返回失败时：存储保持不变，若 `Physical < High`
  （不越界）则沿用旧上界照常发放；若 `Physical >= High`（越界）则
  本次请求失败。
- 续写写入因存储中任期更大而被拒绝时，立即将本节点降为从节点。
- 越界禁止保证：无论续写成功与否，发出的物理部分永远小于已持久化上界，
  因而绝不会发出旧主/他主"可能发出"的值。

### 接任起点的推导

`TakeOver(term)` 以一次原子读后写完成，且必须满足 `term > 存量任期`：

1. 起始物理部分 `start = max(本地时钟, 存量 High)`。
2. 新上界 `High' = start + W` 与新任期一起原子落盘。
3. 首个时间戳为 `(start, 0)`。

旧主可能发出的最大物理部分为 `存量 High - 1`，而
`start >= 存量 High`，故新主的任何时间戳都严格大于旧主可能发出的全部
时间戳——即使新主本地时钟落后或发生回拨。失败/被拒绝的接任不改变
节点身份、不改变存储，也不消耗时间戳。

### 错误原因（可区分）

- `ErrInvalidConfig`：`L` 或 `W` 非正。
- `ErrInvalidCount`：`n` 非正或 `n > L`。
- `ErrNotLeader`：向从节点请求时间戳（含续写时发现任期被超过而降级）。
- `ErrTermNotHigher`：接任任期不大于存量任期。
- `ErrExceedsBound`：续写失败且发放会越过已持久化上界。

### 确定性与并发

- `Clock` 与 `ExtendFault` 均可注入；相同的时钟、故障注入与请求序列
  重放结果完全相同（见 `TestDeterministicReplay`）。
- `Allocate` 在节点锁内完成判定、续写与游标推进，可被多 goroutine
  并发调用；全部时间戳全局唯一，同一主节点内按发放顺序严格递增。
- 日志会打印每次调用的输入、输出与判定依据（物理部分来源、上界、
  续写/越界/任期判定等）。

### 本地验证

```bash
# 全量测试
go test ./...

# 竞态检测 + 详细日志（含输入/输出/判定依据）
go test -race -v ./tso

# 并发稳定性重复执行
go test -race -count=20 -run TestConcurrent ./tso

go vet ./...
gofmt -l .
```

测试覆盖：新主时钟落后旧主 5 秒首签仍更大、逻辑用尽跨毫秒、
批量整体移到下一毫秒、续写故障（不越界/越界）、续写被超任期降级、
时钟回拨、并发唯一性与连续性、各类可区分拒绝原因、确定性重放。

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
