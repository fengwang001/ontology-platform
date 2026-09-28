# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 双流区间连接器 `intervaljoin`

`intervaljoin` 包把左右两条各自按事件时间非递减到达的事件流，按连接键与
**两端闭合**的事件时间区间两两配对。

- **匹配条件**：同键且 `a.Lo <= b.Hi && b.Lo <= a.Hi`，端点相切也算匹配
  （`[0,10]` 与 `[10,20]` 在 `t=10` 配对）。
- **水位线**：每侧独立单调前进，事件到达即把本侧水位线推进到其 `Lo`；
  同侧 `Lo` 倒退以 `time_regression` 拒绝。
- **清理规则**：每步配对后按对侧水位线 `W` 清理两侧，仅当 `Hi < W`（严格
  小于）才丢弃；`Hi == W` 因闭合端点仍可能匹配而保留。
- **拒绝原子性**：非法配置/事件、空键、时间倒退、一步处理后保留数超
  `MaxRetainedPerSide` 都会返回带不同错误码的 `*JoinError`，且不改变水位线、
  编号、保留状态与已输出配对。
- **并发与确定性**：`Process` 写锁串行，`Pairs/Retained/Watermark` 读锁返回
  深拷贝；同一输入序列反复计算输出完全相同。

判定依据通过日志逐条输出（`SetLogger` 重定向），包括输入、每个候选对的
匹配判定、每条事件的保留/清理原因与每步输出。

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

# 双流区间连接器：常规、竞态、重复运行（确定性）、详细日志
go test -v ./intervaljoin
go test -race -v ./intervaljoin
go test -race -count=10 ./intervaljoin

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
