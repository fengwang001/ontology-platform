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

## 到期优先积分钱包

实现位于 `wallet` 包，入口是 `wallet.New()`：

- `Grant(id int64, amount int64, exp int64, now int64) error`：发放一个不可复用 id 的积分批次。
- `Spend(sid int64, amount int64, now int64) ([]wallet.SpendLine, error)`：按到期先后消费，并返回每批扣减明细。
- `Refund(sid int64, amount int64, now int64) ([]wallet.RefundLine, error)`：按消费单部分或全部退回。
- `Balance(now int64) (int64, error)`：返回 `now` 时仍有效批次的剩余积分总和。

### 有效期与消费次序

- 批次有效区间是 `[grantNow, exp)`；当操作时刻 `now == exp` 时，该批次已经过期。
- `Spend` 和 `Balance` 只统计、扣减满足 `now < exp` 的批次。
- 消费排序为：到期时刻 `exp` 小者优先；`exp` 相同时，发放时刻 `grantAt` 小者优先；仍相同时，按进程内实际发放序号排序。
- 一笔消费可以跨多个批次；先完整计算扣减计划，有效余额足够后才提交，不足时所有批次和消费单保持不变。

### 退回与作废

- `Refund` 从该消费单原始扣减明细的最后一批开始逆序退回。
- 每批最多退回该消费单当时从该批扣走、且尚未退回的数量。
- 支持多次部分退回；每次返回本次涉及的批次、数量和 `Voided` 标记。
- 若退回时目标批次满足 `now >= exp`，本次退到该批的数量标记 `Voided=true`，计入过期丢弃，不增加有效余额。
- 每个批次始终满足：`remaining + deducted - refunded == grantAmount`，其中 `refunded` 同时包含有效退回和作废退回。

### 拒绝顺序

操作只返回按以下顺序遇到的第一个错误：

1. `now` 小于此前任何一次操作的 `now`。
2. 数量非正。
3. Grant 的批次 id 重复，或 Spend 的消费单 id 重复。
4. Refund 的消费单不存在。
5. Refund 数量超过该消费单尚未退回的总量。
6. Spend 时有效余额不足。
7. Grant 时 `exp <= now`，即发放即过期。

被拒绝的操作不会修改批次、消费单，也不会推进时间线。所有公开方法由同一把互斥锁保护，并发调用的结果等价于某个合法串行顺序。

### 本地验证

```bash
# 全量测试
PATH=/usr/local/go/bin:$PATH GOCACHE=/tmp/ontology-go-cache go test ./...

# 竞态检测与逐步输入/输出/判定日志
PATH=/usr/local/go/bin:$PATH GOCACHE=/tmp/ontology-go-cache go test -race -v ./wallet

# 朴素逐步模拟对照
PATH=/usr/local/go/bin:$PATH GOCACHE=/tmp/ontology-go-cache go test -run '^TestNaiveSimulationComparison$' -v ./wallet

# 静态检查
PATH=/usr/local/go/bin:$PATH GOCACHE=/tmp/ontology-go-cache go vet ./...
```

如果 shell 已能直接找到 `go`，可省略 `PATH=/usr/local/go/bin:$PATH`；`GOCACHE` 指向可写目录即可。
