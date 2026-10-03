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

## 块拉取调度器（`blocksched` 包）

`blocksched` 为一个按 B 块切分的文件在多个对端之间决定“每次向谁请求哪一块”，
支持最稀有优先、收尾期重复请求、超时降额与校验失败封禁。全部方法共享一把
互斥锁，可并发调用；时间 `now` 单调不减，调用次序相同则状态完全相同、
结果可精确复现。

### 稀有度与收尾期

- `avail[b]` = 当前未被封禁、未被删除且拥有块 b 的对端数（与块是否完成无关）。
- 新块阶段：存在“未完成、全系统无在途、avail>0”的块时，只在
  “未完成、对端拥有、全系统无在途、该对端对它无失败记录”的块中按
  `(avail 升序, 块号升序)` 选择（最稀有优先）。
- 存在新块但该对端没有可请求的新块时，即使有可重复请求的块也返回空。
- 收尾期：不存在任何新块时才重复请求，候选要求该对端对该块无在途且
  该块在途数 `< M`，按 `(在途数升序, avail 升序, 块号升序)` 选择
  （在途数优先于稀有度）。

### 超时降额与封禁

- 每对端有效并发：`cap = max(1, K - ⌊timeouts/2⌋)`；`Tick(now)` 移除所有
  `now-issued >= T` 的在途请求，每条令该对端 `timeouts +1`，不产生失败记录；
  过期项按 `(issued, id, block)` 升序返回。成功 `Done` 清零该对端 `timeouts`。
- `Done(ok=false)`：永久记录该 `(对端, 块)` 失败，永不再向它请求该块；
  失败块数达到 F 即封禁：移除其全部在途、从 avail 剔除。封禁记录在 `Drop`
  后保留，同名 id 永远不能再次 `AddPeer`。
- `Done(ok=true)`：块完成，取消该块上其余全部在途请求，返回被取消对端 id
  （升序）。

### 错误优先级

- `Next`：`ErrClock > ErrNoPeer > ErrBanned`
- `Done`：`ErrClock > ErrNoPeer > ErrBadArg > ErrNoRequest`
- `AddPeer`：`ErrBadArg > ErrPeerExists > ErrBanned`
- `Have`：`ErrNoPeer > ErrBadArg`

被拒绝（错误或空返回）的调用不改变任何状态。

### 本地验证

```bash
# 全部单元测试（含题目示例、收尾期次序、封禁、取消名额等场景）
go test ./blocksched -v

# 2000 组随机事件序列与朴素参考实现对照（带内部不变量自检）
go test ./blocksched -run TestDifferentialRandom -v

# 打印一条小规模序列的输入、输出与判定依据
go test ./blocksched -run TestDifferentialVerbose -v

# 竞态检测
go test -race ./blocksched
```
