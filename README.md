# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 快照加增量双读一致性读取器（`snapshot` 包）

`snapshot.Reader` 在写入持续进行时冻结快照，并把快照与后续增量合并，回答任意历史位点读。

### 数据结构

- 追加日志：每次 `Write` 产生一条记录，序号 `seq` 从 1 开始、全局单调递增；日志永不因快照清空。
- 快照点 `snapSeq` 与不可变快照副本 `snap`：`Freeze` 冻结当前最大序号，并把区间 `[1, snapSeq]` 内每个键的最新值固化进一份全新 map；旧副本不被原地修改。

### 位点区间与边界

设最近快照点为 `S`，读位点为 `P`（位点含义为“截至第 P 次写入，含第 P 次”），当前最大序号为 `N`：

| 区间 | 覆盖来源 | 说明 |
| --- | --- | --- |
| `[1, S]` | 快照副本 | `P == S` 是合法读，纯走快照基，不应用任何增量。快照点恰好等于某次写入时，该次写入已包含在快照内。 |
| `(S, P]` | 增量日志 | `P > S` 时，从快照基出发按序应用序号 `S+1 … P` 的记录，同一键最新写胜出。 |
| `(S, S+1]` 为空 | 仅快照 | `P == S+1` 且其间无写入时，结果与 `P == S` 相同。 |
| `P < S` | 拒绝 | 返回 `ErrReadBeforeSnapshot`：位点早于最近快照点，该段历史已不能用“当前快照 + 其后增量”还原。 |
| `P > N` | 读到 `N` | 未来尚无写入，等价于按位点 `N` 回答（结果中的 `Position` 仍回显请求值）。 |

反复快照会推进 `S`：一旦重新 `Freeze`，此前早于新 `S` 的位点同样按 `ErrReadBeforeSnapshot` 拒绝。

### 失败原因（可 `errors.Is` 区分，且失败不改动任何状态）

- `ErrEmptyKey`：读或写使用空键。
- `ErrEmptyValue`：写入空值。
- `ErrReadBeforeSnapshot`：读位点早于最近快照点（错误信息附带请求位点与快照点）。
- `ErrInvalidPosition`：读位点为负数。
- `ErrSnapshotMismatch`：`Verify` 自检发现快照与日志重放不一致。

任一失败调用都不会改变日志、快照点与快照内容。

### 并发语义

- `ReadAt` 与 `Verify` 可与 `Write`/`Freeze` 以及彼此并发调用（读写锁保护；快照通过整表复制实现不可变）。
- 同一实例、同一键、同一位点的并发读返回的 `ReadResult` 逐字段相同，不会读到快照/增量合并的中间态。

### 本地验证：按序号从头重放核对

双读结果必须等价于“把日志从 seq=1 按序重放到位点 P，逐键取最新值”。两种核对方式：

1. 自检 API：调用 `r.Verify()`，它在锁内把 `r.log[0:snapSeq]` 从头重放成 map，与不可变快照逐键逐值比对，并校验序号连续性。
2. 测试 oracle：测试中的 `replayUntil(log, key, position)` 实现同一份重放语义，每个成功读都与它比对；并发测试在同一把读锁的同一线性化点上执行双读与重放，避免测试自身读到中间态。

```bash
# 含竞态检测的完整验证
go test -race -v ./snapshot
go vet ./...
```

单测日志（`-v`）打印每次操作的输入、快照点 `snapshotPoint`、读位点 `readPosition`、返回值与判定依据 `basis`。

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
