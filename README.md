# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## replication：基于领导者世代的跟随者日志截断

`replication` 包实现领导者更替后的日志恢复：跟随者按世代缓存多轮截断分叉尾部，
再从领导者复制追平，恢复后各副本与领导者逐位点一致。

### 世代缓存

- 每个副本维护 `[]EpochMark{Epoch, Start}`，表示世代 `Epoch` 从位点 `Start` 开始。
- 追加缓存项的时机：`AppendLeader` 写入比当前最新世代新的消息时，以及 `BecomeLeader` 当选时（起始位点为当前日志末尾）。
- 截断时同步删除起始位点不小于截断点的缓存项，缓存与日志始终一致。

### 领导者世代响应

`EpochResponse(leaderID, epoch) (end, known, err)`：

- 世代在领导者缓存中：返回其结束位点（下一世代起始位点，无更大世代时为日志末尾），`known=true`。
- 世代比领导者最新世代新或落在缓存空洞中：`known=false`，跟随者应截掉整个未知世代。
- 世代早于领导者最早缓存项：返回 `ErrUnknownEpoch`。

### 截断与恢复规则

- `TruncateRound` 执行一轮截断，只截断不拉取：世代已知时截断点取
  `min(世代响应位点, 跟随者结束位点)`；世代未知时截断点取该未知世代的起始位点。
  多轮调用收敛到与领导者的最长公共前缀。
- `Recover` 先在状态副本上模拟多轮截断并校验公共前缀，确认可恢复后才一次性应用截断，
  再把领导者尾部复制给跟随者；恢复后逐位点一致，再次恢复为幂等空操作。
- 截断只删除分叉尾部，前缀永不越过最长公共前缀；追平由恢复复制完成。

### 边界与错误类别

所有拒绝都是整体的、不留痕的（不改变任何副本日志或世代缓存），且原因互相可区分：

- `ErrInvalidArgument`：空 id、重复 id、世代不增、冒名写入、旧世代写入、自我恢复、缓存与日志不一致的登记等。
- `ErrUnknownReplica`：请求引用了集群中不存在的副本。
- `ErrUnknownEpoch`：请求世代早于领导者最早缓存世代。
- `ErrLogDiverged`：截断收敛后公共前缀内仍存在同位点条目分叉（非法状态）。

### 并发

`EpochResponse`、`Replica.Log/End/EpochCache` 查询与对不同跟随者的 `Recover`
可被多个执行体并发调用；副本级读写锁按 id 字典序成对获取，无死锁。

### 本地验证

```bash
go test -race -v ./replication   # 截断/恢复/拒绝/并发用例，日志打印每步输入、截断点与判定依据
go vet ./... && gofmt -l .
```

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
