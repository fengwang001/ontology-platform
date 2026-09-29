# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## replication：基于领导者世代的跟随者日志截断

`replication` 包解决领导者更替后的分叉恢复：旧领导者可能已向部分跟随者
复制了分叉条目，新领导者恢复跟随者时**只截断、不拉取**，把跟随者日志
尾部的分叉部分截到与领导者的最长公共前缀（LCP），追平由后续复制完成。

### 世代缓存

每个副本维护有序表 `[]GenMark{Gen, Start}`（世代号 → 该世代第一条消息的
起始位点）。以下时机会追加缓存项：

- `BecomeLeader`：副本当选新领导者，在当前日志末尾登记新世代；
- `Append`：领导者写入新世代消息；
- `Replicate`：跟随者复制到新世代消息（在该消息位点自动登记）。

世代号必须严格递增，起始位点必须落在登记时的日志末尾。截断
`truncateAt(pos)` 会同时删除所有 `Start >= pos` 的缓存项（含空世代
标记），保证缓存与剩余日志一致。

### 领导者响应与恢复规则

- `GenerationEnd(leader, gen)`：按请求世代返回该世代可用日志的**独占
  结束位点**；恢复时跟随者还会携带自己缓存的起始位点，同号世代但起始
  位点不同（同号不同分叉）同样视为未知世代。
- `RecoverFollower(leader, follower)` 多轮执行，每轮取跟随者最新世代：
  1. 领导者不认识该世代 → 删除该缓存项，日志截到该世代 `Start`，进入下一轮；
  2. 领导者认识 → 在 `[Start, End)` 逐位点比较，第一条分歧即截断点；
  3. 跟随者比领导者长 → 截到领导者的结束位点；
  4. 完全一致 → 结束，返回截断点（等于与领导者的朴素逐位点 LCP 长度）。
- 恢复后 `Replicate(leader, follower, at)` 必成功；再次 `RecoverFollower`
  为幂等空操作。

### 边界与错误类别

所有输入错误都是**整体拒绝、失败不留痕**：任一参数不合法时，任何副本的
日志与世代缓存都不发生变化（写操作先完整校验再变更）。四类可区分错误：

- `ErrInvalidArgument`：空副本名、世代 `<=0`、世代未严格递增、位点越界、
  范围反向、源与目标同名等；
- `ErrUnknownReplica`：引用未注册副本；
- `ErrUnknownGeneration`：副本缓存中不存在该世代，或同号世代起始位点
  不一致（不同分叉分支）；
- `ErrLogDiverged`：同一位点的 `(Gen, Data)` 不一致，或跟随者比复制源
  更长。

并发：世代响应、日志查询为只读，可多执行体并发；涉及多个副本的操作按
副本名排序加锁以防死锁，单次恢复全程持有领导者与跟随者锁，期间不会
读到“截了一半”的状态。每一步输入、截断点与判定依据通过 `slog` 打印
（`recover start` / `recover round` / `recover done`，字段含 `gen`、
`leader_has_gen`、`gen_end`、`cut`、`cut_len`、`reason`）。

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
go test -race -v ./replication

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
