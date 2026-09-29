# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 版本向量增量反熵同步（`antientropy` 包）

`antientropy` 提供多副本间最终一致的键值同步：每个副本只累积本地写入，
通过两两（双向）同步交换增量，任意图连通后各副本的向量、日志、读视图完全一致。

### 版本向量与变更标识

- 变更 `Change{Source, Seq, Key, Value, TS}` 由 `(Source, Seq)` 全局唯一标识。
- 每个来源的序号从 1 开始、严格连续、只增不减。
- 版本向量 `Version` = 来源名 -> 该来源“已连续应用到”的最大序号；
  它只记录真正持有过变更的来源——从未见过的来源既不产生变更，也不写入向量
  （即使对端发来的向量里带有未知来源条目，也直接忽略）。

### 本地写入

- `Replica.Write(key, value)`：本来源序号 +1，逻辑时钟 +1 生成时间戳 `TS`，
  变更追加到来源子日志与全局日志（始终按 `(Source, Seq)` 字典序有序），
  并把本来源的向量条目推进到新序号。

### 同步规则（`antientropy.Sync(src, dst, log)`）

1. `dst` 把自己的版本向量发给 `src`。
2. `src` 对每个自己持有日志的来源，取 `Seq > dst 已见值` 的变更——这是差集最小的关键：
   已见变更绝不重发，未知来源不产生条目。差集整体按 `(Source, Seq)` 有序。
3. `dst` 应用前整体校验：每个来源的序号必须相对自己的已见值严格连续（1,2,3…，
   不缺号、不重复、不乱序），来源必须已注册。
4. 校验通过后一次性原子应用（日志 + 向量同时推进）；任何失败都整体拒绝，不留痕。
5. `log` 非 `nil` 时逐步打印目标向量、差集判定依据、每条发送的变更与应用后新向量。

双向同步即对两个方向各调用一次 `Sync`。

### 读视图取值

- `Replica.View()` 对每个键，在所有已应用变更中取 `(TS, Source, Seq)`
  字典序最大者的值。先比时间戳；时间戳相同再比来源名，再比序号，因此结果全序、可复现。

### 边界与错误类别

四类错误互不相同，可用 `errors.Is` 区分；任一错误发生时双方日志与向量均不变：

- `ErrUnregisteredReplica`：同步一方未注册、双方不属于同一注册表、变更来源未注册、重名/空名注册。
- `ErrNonContiguousChange`：序号缺号、重复、非正数、批次乱序，或与接收方已见序号不衔接。
- `ErrInvalidVector`：版本向量中出现负序号条目。
- `ErrLogLimitExceeded`：本次差集条数超过 `Config.MaxBatch`（0 表示不限；取双方更严格上限）。

### 并发与收敛

- 每个副本一把互斥锁；`Sync` 按副本名字典序固定加锁顺序，避免双向/并发同步死锁。
- 任意一组副本在任意交错的并发写入与同步后，做完全连接同步即收敛到相同向量、日志与视图。
- 测试中以“朴素整份日志逐条发送”为参照，校验增量机制与其最终状态完全一致，
  并校验差集最小（已同步后差集为空、部分已知时只发缺口之后的变更）。

### 本地验证

```bash
# 常规测试
go test ./antientropy

# 竞态检测 + 详细日志（打印每步同步向量、差集与判定依据）
SYNC_STEPS=1 go test -race -v ./antientropy

# 覆盖率
go test -cover ./antientropy
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
