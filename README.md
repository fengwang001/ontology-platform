# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 跨站点分布式死锁检测器（`deadlock` 包）

锁分属各站点，每个站点只维护本地锁表与本地等待边；通过沿等待边转发
探测消息（edge-chasing probe）发现跨站点等待环。

### 锁与等待

- 锁有共享（S）/排他（X）两种模式；仅 S+S 相容。
- 请求与持有者不相容、或队列非空时一律排队（FIFO）；等待边指向全部
  不相容持有者与排在前面的一切不相容等待者。
- 再次请求自己已持有的锁视为成功（无操作）。
- 释放/中止后按 FIFO 授予队列前缀中可相容的等待者。

### 探测转发与丢弃规则

事务开始等待时，沿自己的每条等待边发出探测。探测携带发起者与途经
事务序列 `path`，经注入延迟、可乱序投递。投递到事务 `cur` 时：

1. 所沿的最后一条等待边 `prev->cur` 此刻已不存在 → 丢弃（等待已解除，
   不得误判）。
2. `cur` 已不在等待 → 丢弃。
3. `cur` 已出现在 `path` 中且不是发起者 → 丢弃（进入了不含发起者的环；
   路径长度有界，保证终止，不会无限转发）。
4. 否则把 `cur` 追加到 `path`，转发给 `cur` 当前等待的每个事务。

### 死锁确认条件

探测回到发起者时，必须逐条重新确认途经的每条等待边**此刻仍存在**，
全部存在才判定死锁；任一边消失则丢弃。因此被中止的事务在判定时刻
必定处在真实存在的等待环上，已解除的等待不会被误判。

### 牺牲者选择

牺牲者为环上开始时间戳最大（最年轻）的事务；中止后释放其全部锁并按
FIFO 授予等待者。中止是幂等的：多个发起者同时发现同一环时只会真正
中止一个事务。

### 校验与拒绝

请求不存在的站点/锁、已结束或正在等待的事务再发请求、释放未持有的
锁、开始时间戳重复等，均整体拒绝并返回可区分的哨兵错误
（`ErrUnknownSite`、`ErrUnknownLock`、`ErrTxnFinished`、
`ErrTxnWaiting`、`ErrLockNotHeld`、`ErrDuplicateStartTS` 等），
被拒绝的操作不改变锁表与等待关系。

### 并发与确定性

`Request`/`Release`/`End`/`DeliverOne`/`DeliverAll` 均可并发调用
（内部串行化）。网络延迟由种子驱动的伪随机数注入，投递按
`(arrival, seq)` 排序，因此相同操作与投递序列重放得到相同的牺牲者
序列。

### 本地验证

```bash
# 全部测试（三站点成环、共享锁多持有者环、在途探测丢弃、
# 双发起者只中止一个、不含发起者的环终止、FIFO、校验拒绝、
# 重放确定性、并发）
go test ./deadlock/ -v

# 竞态检测
go test -race ./deadlock/
```

测试日志打印输入（`[REQUEST]`/`[RELEASE]`/`[END]`）、输出
（`[GRANT]`/`[QUEUE]`/`[ABORT]`）与判定依据
（`[DEADLOCK] cycle=... all N edges verified present; victim=...`、
`[DROP] ... reason=...`）。

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
