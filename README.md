# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 检查点协调器（`checkpoint` 包）

`checkpoint.Coordinator` 为固定 N 个任务驱动检查点的触发、确认、超时、
吞并、保留与恢复。所有方法可并发调用，内部以单一互斥锁线性化，因此确认与
超时任意交错的结果都与把操作按该顺序逐事件串行推演完全一致。

### 时钟与编号

- 虚拟时钟从 0 开始，单位为 `time.Duration`；只能通过 `Advance(now)` 前进，
  `now < 当前时钟` 整体拒绝（`ErrClockBackward`）且不改变任何状态，相等允许。
- 超时只在 `Advance` 内判定：当时钟 `>= 触发时钟 + Timeout` 时中止；同一次推进
  内对推进开始时仍在进行的检查点按编号升序逐个处理。
- `Trigger` 在当前时钟分配编号，从 1 起严格递增、连续无空洞、永不复用；被拒绝的
  触发不占号（`NextID` 不变）。

### 状态迁移与计数规则

- 进行中（inflight）→ 完成：收到 N 个**不同任务**（任务编号 `0..N-1`）的确认。
  - 完成时：连续超时数清零；完成时刻锚定最小间隔；编号更小的进行中检查点全部以
    “吞并”（swallowed）中止；编号更大的进行中检查点不受影响。
  - 完成的检查点加入保留集，仅保留编号最大的 R 个；被淘汰编号仍可被识别为
    “已结束”（与“编号不存在”区分）。
- 进行中 → 中止，四种理由互斥，每个检查点恰好终局一次：
  - `timeout`：推进时到达超时边界；连续超时数加一。
  - `swallowed`：被更小编号先完成的检查点吞并；不计数。
  - `recovery`：恢复时仍在进行；不计数。
  - `failed`：协调器转失败后其余进行中检查点的中止理由；不计数。
- 连续超时数 **超过** `ToleratedTimeouts`（即 `> T`）时协调器转为失败；触发该
  转换的那次超时本身仍计为超时，其余进行中检查点在同一次推进内以 `failed` 中止、
  不计数。失败后 `Trigger` 一律返回 `ErrFailed`。
- `Recover` 返回保留集中编号最大者；中止全部进行中检查点（理由 `recovery`，不
  计数）、清零连续超时数、使失败的协调器回到正常；保留集不变，编号继续递增。

### 触发条件（按此顺序判定）

协调器未失败 → 进行中数量 `< MaxInflight` → 距最近一次**完成**时钟差
`>= MinInterval`（从未完成过则不限制间隔；吞并等中止时刻不锚定间隔）。

### 错误优先级（只报第一个；拒绝不改变状态）

- `Advance`：`ErrClockBackward`。
- `Trigger`：`ErrFailed` > `ErrConcurrencyFull` > `ErrIntervalTooShort`。
- `Ack(id, task)`：`ErrTaskOutOfRange` > `ErrUnknownCheckpoint` >
  `ErrCheckpointEnded` > `ErrDuplicateAck`。
- `Recover`：`ErrNoCheckpoint`。
- `New`：配置非法时返回包装了 `ErrInvalidConfig` 的错误。

### 查询与日志

- `Query()` 返回深拷贝快照 `Snapshot`（时钟、失败标志、`NextID`、连续超时数、
  进行中/保留编号列表及每个检查点的视图），可在并发下安全持有。
- 每个操作都通过 `slog` 打印输入、输出与判定依据（`basis` 字段），可用
  `Config.Logger` 注入；`go test -v` 时直接输出到控制台。

### 本地验证

```bash
# 常规测试（日志打印每个操作的输入/输出/判定依据，需 -v 查看）
go test -v ./checkpoint

# 竞态检测（覆盖并发触发上限与并发确认恰好成功一次）
go test -race -v ./...

# 静态检查与格式
go vet ./...
gofmt -l .
```

若环境的 Go 构建缓存目录只读，可指定可写缓存，例如
`GOCACHE=/tmp/gocache go test -race ./...`。

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
