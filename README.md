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

## 批量写入组提交器（`groupcommit`）

`groupcommit` 包把大量并发写请求合并成批次，经单个后台 worker 串行持久化，
并把每条请求的结果准确送回其调用方。

**组批规则**
- 待写请求在单一 FIFO 队列上按进入顺序排队；同一时刻至多一个批次在持久化。
- 上一批结束后立即从队首切新批：持续取请求，直到达到条数上限（`MaxEntries`），
  或再加一条将使批次总字节数超过字节上限（`MaxBytes`）为止；队首即满则立即成批。
- 单条负载字节数大于 `MaxBytes` 时在入队前直接拒绝，因此不会出现“批次里只有一条
  却超限”的情况；恰等于上限允许通过。

**序号分配时机**
- 序号只在批次切分完成、交给持久化之前，按该批的队列顺序连续分配（从 1 开始）。
- 持久化成功后该批所有条目各自拿到自己的序号与批次号；已持久化序号始终从 1 起
  连续、无空洞、无重号。

**失败与回收**
- 持久化失败时整批失败：批内每条请求收到完全相同的失败原因
  （`ErrBatchFailed` 包装底层错误），不允许部分成功。
- 失败批次的序号段整体回收：下一批从该段起始序号重新分配。因为失败批次对存储
  完全不可见，持久化结果仍满足 1..N 连续。某批失败不影响后续批次。
- 批次不自动重试；失败语义直接返回给调用方，由调用方决定是否重新提交。

**立即拒绝（可区分原因，不占序号、不入队）**
- 空负载：`ErrEmptyPayload`
- 单条超过字节上限：`ErrPayloadTooLarge`
- 关闭后提交：`ErrClosed`
- 创建参数非正或缺失：`ErrInvalidMaxEntries`、`ErrInvalidMaxBytes`、`ErrNilPersister`

**关闭语义**
- `Close` 先停止接收新请求，再把关闭前已入队（含已在提交通道缓冲中）的请求
  全部组批、持久化并送达结果，全部完成后才返回；`Close` 可重复调用。
- 与 `Close` 竞争且未被 worker 接收的提交会收到 `ErrClosed`。
- `Submit` 的 `ctx` 被取消时仅表示该调用方不再等待；其请求仍随所在批次持久化，
  结果写入每条请求专属的缓冲结果通道，绝不串台、不丢失、不阻塞 worker。

**可复现性**
- 给定相同的入队顺序与相同的故障注入（第 k 批失败），批次划分、序号分配与
  成功/失败结果完全相同；批次切分只依赖队列顺序与条数/字节上限。

**日志**
- 配置 `Config.Log`（`Printf` 风格）后，日志覆盖每次提交的输入（字节数、拒绝原因）
  与每个批次的判定依据：条数/字节截批、序号区间、提交推进或失败回收。

**本地验证**

```bash
# 全量测试（含 200 并发调用方、第 k 批失败序号连续性、条数/字节截批边界、
# 单条超限、关闭时在途请求、可复现性等用例）
go test -race -v ./groupcommit/

# 反复运行以压制调度抖动
go test -race -count=20 ./groupcommit/

# 全仓库检查
gofmt -l .
go vet ./...
go test -race ./...
```
