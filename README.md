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

## 块拉取调度器

`BlockScheduler` 为按块切分的文件维护可复现的块请求调度。所有方法由同一把互斥锁保护；调用方法的线性化次序相同时，状态和返回结果相同。

### 调度规则

- `avail[b]` 统计当前未删除、未封禁且拥有块 `b` 的对端数；块是否完成不影响该统计。
- 新鲜块阶段：只要存在“未完成、当前零在途、`avail > 0`”的块，`Next` 只考虑零在途块，按 `(avail, block)` 升序选择。
- 若全局仍有新鲜块，但当前对端没有可请求的新鲜块，即使存在可重复请求的块，也返回空结果。
- 收尾阶段：没有全局新鲜块时才允许重复请求，候选按 `(该块在途数, avail, block)` 升序选择，因此先补在途最少的块，再比较稀有度。
- 每个对端的新请求容量为 `cap = max(1, K - floor(timeouts/2))`；容量下降时不取消已在途请求，只限制新分配。成功完成一块会清零该对端的 `timeouts`。
- `Tick` 移除所有满足 `now-issued >= T` 的请求，按 `(issued, id, block)` 升序返回；超时不会写入失败记录，因此该块之后仍可再次分给同一对端。
- 校验失败会永久阻止该对端再次请求同一块；累计失败数达到 `F` 时封禁该对端，封禁记录在 `Drop` 后仍保留，不能再次 `AddPeer`。
- `Done(ok=true)` 完成块后取消其他对端在同一块上的请求，并按对端 ID 升序返回被取消者。

### 错误优先级

- `Next`：`ErrClock` → `ErrNoPeer` → `ErrBanned`，之后才检查容量和候选块。
- `Done`：`ErrClock` → `ErrNoPeer` → 块号越界 `ErrBadArg` → `ErrNoRequest`。
- `Have`：先检查对端是否存在（`ErrNoPeer`），再检查块号（`ErrBadArg`）。
- `AddPeer`：先检查空 ID 或 `have` 长度（`ErrBadArg`），再检查现存对端（`ErrPeerExists`），最后检查永久封禁（`ErrBanned`）。
- `Next`、`Done`、`Tick` 的 `now` 必须不小于此前任何一次调用的时间，否则返回 `ErrClock` 且不修改状态。

### 调度器本地验证

```bash
# 全量测试（包含 2000 组随机事件与朴素规则模型对照）
go test -v ./...

# 竞态检测
go test -race ./...

# 静态检查
go vet ./...
gofmt -w scheduler.go scheduler_test.go scheduler_random_test.go scheduler_concurrency_test.go
```

随机对照测试在 `-v` 日志中打印每个事件的输入、返回结果以及采用该判定的规则依据。
