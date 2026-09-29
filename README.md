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

## 副本组闭合时间戳跟踪

`closure` 包提供可注入时钟与网络的内存模拟器，用于验证主副本持续发布闭合时间戳后，从副本如何在本地服务历史读。

### 闭合时间戳

- 初始闭合时间戳为 `-1`，表示尚未承诺任何非负读时间戳。
- 每次发布先计算候选值 `candidate = injectedClock - targetLag`。
- 候选值必须严格小于主副本所有在途写的最终时间戳；只要存在某个在途写满足 `candidate >= writeTimestamp`，候选值被拒绝。
- 候选值不大于上次发布值时也不会前进。闭合时间戳因此单调不减。
- 发布体为 `(ClosedTimestamp, LogSequence)`，其中 `LogSequence` 是发布瞬间已分配的最大连续日志序号；没有写时为 `0`。
- 写在主副本 `Write` 提案时获得连续序号并进入在途集合，主副本按序 `Deliver` 应用后离开在途集合。

### 写时间戳抬高

- 写输入时间戳必须非负；目标滞后必须非负；操作目标副本必须存在。
- 若写的期望时间戳满足 `desired <= closed`，最终时间戳抬高为 `closed + 1`。
- 否则使用期望时间戳。`WriteResult.Raised` 表示是否发生抬高。
- 该规则保证每个写的最终时间戳都大于其提案前发布过的所有闭合时间戳。

### 从副本应用与本地读

- 写消息和发布消息都通过 `Deliver` 注入，调用方可以乱序、重复投递。
- 写按日志序号连续应用；序号缺口放入 `pending`，缺口补齐后自动批量应用。
- 同序号重复写必须内容一致；发布消息按 `(ClosedTimestamp, LogSequence)` 去重。
- 读时间戳 `t` 可服务，当且仅当从副本收到过至少一条满足 `t <= ClosedTimestamp` 且 `applied >= LogSequence` 的发布。
- 可服务时必须服务，返回该 key 时间戳不大于 `t` 的最新版本；同时间戳的多个版本按日志序号取后者。
- 若没有任何已收到发布满足 `ClosedTimestamp >= t`，返回 `ErrNotClosed`；已有这种发布但对应日志尚未应用完，返回 `ErrNotCaughtUp`。
- 负滞后返回 `ErrInvalidTargetLag`，负时间戳返回 `ErrNegativeTimestamp`，不存在副本返回 `ErrReplicaNotFound`；这些错误可通过 `errors.Is` 区分，被拒绝操作不修改状态。

### 本地验证

```bash
# 包测试，输出每个场景的输入、输出与判定依据
go test ./closure -v

# 竞态检测
go test -race ./closure

# 全量验证
go test ./...
go vet ./...
gofmt -l .
```

测试覆盖在途写阻止闭合越过它、`desired == closed` 被抬高、从副本落后时使用较早发布服务历史读，以及固定种子随机重放并与主副本最终读对拍。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
