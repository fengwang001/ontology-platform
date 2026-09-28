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

## 幂等落库组件（`dedup` 包）

`dedup.Sink` 把至少一次（at-least-once）投递的批次按分区与位点去重后累加到结果表，
保证崩溃或重投后结果既不漏计也不重复计。

### 水位与去重规则

- 每个分区独立维护一个**水位**：该分区已生效记录的最大位点，初始为 `-1`。
- 记录位点 `offset <= 水位` 判为**重复**，直接丢弃并将重复计数加一；
  `offset > 水位` 时才生效：值累加到结果表对应键，并把该分区水位推进到该位点。
- 同一批内同一分区的位点必须**严格递增**（相等或逆序均拒绝），否则整批拒绝。
- 以下输入整批拒绝（可用 `errors.Is` 区分原因），且不改变结果表、水位与重复数：
  - `ErrNegativePartition`：分区号为负
  - `ErrPartitionLimit`：分区号超过 `Open` 配置的分区数上限
  - `ErrNegativeOffset`：位点为负
  - `ErrEmptyKey`：结果表键为空
  - `ErrOutOfOrder`：同批同分区位点未严格递增

### 原子提交与重启

- 每批在互斥锁内完成校验、累加、水位推进与持久化；结果表、全部分区水位、
  重复计数作为一个整体写入状态文件（临时文件 + `fsync` + 原子 `rename` + 目录 `fsync`）。
- 持久化失败时新状态不会替换旧状态，对外仍保持上一批的已提交状态。
- `Open` 时若状态文件存在，**仅从该文件重建**内存状态，不依赖任何其他来源；
  因此重投的批次在重启后仍被判重，崩溃前未提交的批次不会生效。

### 并发与确定性

- `Apply` 可被多 goroutine 并发调用，每批整体原子；并发结果与只写一次完全一致，
  重复计数恰好等于多写（判重丢弃）的次数。
- 同一输入序列在空状态上反复重放，得到完全相同的结果表、水位与重复计数。

### 日志

使用 `log/slog` 记录：每批输入（记录数）、每条记录的**生效/重复**判定及其依据
（如 `offset > 水位` 或 `offset <= 水位`）、整批提交结果或拒绝原因。

### 本地验证

```bash
# 仅运行 dedup 包测试（详细输出，可观察判定日志）
go test -v ./dedup

# 竞态检测（验证并发幂等性）
go test -race ./dedup

# 全量检查
gofmt -l .
go vet ./...
go test ./...
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
