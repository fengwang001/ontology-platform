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

## 订阅增量推送器（`subscription` 包）

按谓词条件过滤的并发安全订阅推送器，把变更流中“键 >= 下界”的变更增量推
送给活跃订阅。代码见 `subscription/pusher.go`。

### 增量语义

- 每条变更在 `Push` 时按调用顺序分配全局序号，序号从 1 开始、连续递增、
  无洞；`Position()` 返回已投递变更总数（当前全局位点）。
- `Subscribe(id, lowerBound)` 返回注册生效位点 `startSeq`：只有注册之后到
  达（序号 `seq > startSeq`）且命中条件的变更才会推送。注册之前已经投递
  的历史变更即使命中也**不补推**。
- `Unsubscribe(id)` 退订后该订阅立即从活跃集合移除，之后的任何 `Push` 都
  不会再向它投递（退订零推送），`DeliveredChanges` 对其返回 `nil`。

### 边界判定规则

- 命中条件为 `Key >= lowerBound`，采用 Go 字符串字典序比较；**键恰好等于
  下界时必须命中**（含等号），仅严格小于下界才不命中。
- 判定与序号分配在同一把写锁内原子完成，因此每个订阅收到的通知集合和
  序号顺序在并发下仍然确定、可复现。
- 非法输入返回三类互不相同、可用 `errors.Is` 判定的哨兵错误，且失败不改变
  任何状态：
  - `ErrInvalidSubscriptionID`：订阅标识为空或全空白；
  - `ErrDuplicateSubscription`：用同一标识重复订阅；
  - `ErrSubscriptionNotFound`：退订未注册（含已退订）的标识。

### 观测接口

- `Snapshot()`：并发读取活跃订阅的 `{startSeq, lowerBound}` 快照；
- `DeliveredChanges(id)`：并发读取某订阅已收到的通知（拷贝，含序号、键值
  与命中所用下界）；
- 所有订阅、退订、推送操作通过 `slog` 记录输入参数、结果（位点/命中订阅
  列表）与判定依据（`rule`/`reason` 字段）。

### 本地验证

```bash
# 全量测试（竞态检测 + 详细日志，日志含输入、结果与判定依据）
go test -race -v ./subscription

# 反复运行确认并发场景稳定可复现
go test -race -count=10 ./subscription

# 全仓检查
go test ./...
gofmt -l .
go vet ./...
```

测试覆盖：边界等号命中（`TestBoundaryEqualityHits`）、注册后不补推历史
（`TestNoBackfillBeforeSubscribe`）、退订后零推送
（`TestUnsubscribeZeroPush`）、三类非法输入被拒且状态不变
（`TestRejectedInputsKeepState`）、8 协程 1000 条并发推送后位点等于推送
总数且各订阅命中集合精确（`TestConcurrentPushPositionAndDelivery`），以及
订阅/退订/推送并发无竞态（`TestConcurrentSubscribeUnsubscribeAndPush`）。
