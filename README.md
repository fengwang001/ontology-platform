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

## 刀具库寿命管理服务（`toollife`）

多加工通道并发申请、寿命预占/记账、破损换新、锁定与预警的确定性服务，位于
[`toollife/`](toollife)，设计与取舍见 [`docs/design.md`](docs/design.md)。

### 模块划分

- `toollife/types.go`：值对象与枚举（寿命口径、选刀模式、刀具状态、输入输出视图）。
- `toollife/errors.go`：六类可区分错误码（参数非法 > 不存在 > 冲突 > 状态不允许 > 无刀可用 / 暂无余量）。
- `toollife/group.go`：刀组内核——顺序选刀、余量判定与失败原因分类（纯逻辑）。
- `toollife/service.go`：并发安全门面——校验、加锁串行化、申请编号台账与生命周期流转。

### 主要 API

| 方法 | 说明 |
| --- | --- |
| `AddGroup(id, GroupConfig)` | 注册刀组（寿命口径/上限/预警千分比/严格或宽松/刀具顺序） |
| `Apply(groupID, requestID, estimated)` | 顺序选第一把「可用且可承载」的刀并预占；重复编号回放原结果，换内容报冲突 |
| `Settle(requestID, actual)` | 预占转已用、差额释放；幂等；仅记账触发预警与耗尽 |
| `Cancel(requestID)` | 中止并释放预占，不计消耗（幂等） |
| `ReportBroken` / `Replace` | 破损（预占保留待结算）/ 仅破损且无未结算预占时可换新 |
| `Lock` / `Unlock` | 锁定不参与选刀且保留预占；达限刀解锁落为已耗尽 |
| `Query(groupID)` | 每把刀状态/已用/预占/剩余（非负）及当前会被选中的刀 |

错误通过 `toollife.CodeOf(err)` 取码区分；`ErrNoTool`（已耗尽/破损/锁定）
与 `ErrNoMargin`（余量被预占占满，释放后可能成功）明确分离。

### 测试

- 场景单测：预占+已用恰等于上限、严格/宽松差异、实际大于预计、破损刀未结算
  预占、换新拒绝条件、预警恰好跨线且只发一次、暂无余量与耗尽区分、重复申请/记账
  （`service_test.go`、`scenarios_test.go`，`-v` 可看输入/输出/判定依据日志）。
- 随机对照：400 轮随机多通道序列与独立朴素模型逐步比对全部状态（`naive_test.go`）。
- 并发：`-race` 下多通道抢容量（接受数恰等于组容量）与混合操作压力（`concurrent_test.go`）。
- 开销证明：历史申请 100 vs 100000 两档，单次申请耗时比约 1.03（< 2.5 阈值），
  反向刀具数 8→800 对照可见扫描成本随刀具数增长（`scale_test.go`）。

```bash
export PATH=$PATH:/usr/local/go/bin
export GOCACHE=/tmp/gocache   # 仅当默认缓存目录只读时需要
go test -race ./...
go test ./toollife -run TestHistoryScaleIndependent -v
```
