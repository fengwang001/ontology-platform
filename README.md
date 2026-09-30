# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## closedts：副本组闭合时间戳跟踪器

`closedts` 包让主副本在写入进行中持续发布「不会再有时间戳不大于它的写」的承诺，
从副本据此就地服务历史读，且与主副本在同一时间戳的最终结果一致。

### 闭合时间戳计算

- 每次发布的候选值为注入时钟当前值减去目标滞后：`candidate = clock.Now() - lag`。
- 候选值必须小于所有在途（已提案未应用）写的时间戳，否则钳制为 `min(在途写时间戳) - 1`。
- 候选值小于上次发布值时保持上次值，闭合时间戳单调不减。
- 发布内容为 `(闭合时间戳, 此刻已分配的最大日志序号)`，经注入网络广播给全部从副本。

### 写抬高规则

- 写携带期望时间戳提案；若不大于已发布的最大闭合时间戳 `c`，抬高为 `c + 1`。
  因此每个写的最终时间戳大于其提案前发布过的全部闭合时间戳。
- 写在提案时获得连续日志序号并进入在途集合，应用后离开；
  应用后日志经注入网络送达从副本（可乱序、可重复），从副本按日志序号顺序应用。

### 从副本可服务条件

从副本能在读时间戳 `t` 上服务，当且仅当已收到的某条发布 `(Closed, MaxSeq)` 满足
`t <= Closed` 且自身已应用序号 `appliedSeq >= MaxSeq`；能服务就必须服务。
不能服务时：若没有任何已收到发布的闭合时间戳不小于 `t`，报「未闭合」(`ErrNotClosed`)，
否则报「未追上」(`ErrNotCaughtUp`)。

### 参数校验

目标滞后为负（`ErrNegativeLag`）、时间戳为负（`ErrNegativeTimestamp`）、
指向不存在的副本（`ErrReplicaNotFound`）都会整体拒绝并给出可区分原因，
被拒绝的操作不改变任何副本状态。

### 本地验证

```bash
# 全部测试（含在途写阻止闭合、等于 c 抬高、较早发布服务、随机对拍、确定性重放、并发）
go test ./closedts/ -v

# 竞态检测
go test -race ./closedts/
```

测试日志逐条打印每个操作的输入、输出与判定依据（`go test -v` 可见）。

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
