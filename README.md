# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 分片谱系与租约消费协调器（`coordinator` 包）

在整数键空间 `[0,K)` 上管理数据流分片的分裂/合并谱系与消费租约，
保证：任一分片任一时刻至多一个有效持有者；同一键的记录始终按追加顺序被提交消费；
追加不丢不重；同一操作序列重放结果完全相同（确定性状态机，分片 ID 顺序分配）。

### 谱系与排空判定

- 初始只有一个开放分片覆盖 `[0,K)`；追加按键路由到开放分片，返回 `(分片, 位置)`，位置从 0 起。
- 分裂 `Split(id, mid)`：开放分片 `[lo,hi)` 在 `mid`（`lo<mid<hi`）处关闭，
  产生子分片 `[lo,mid)` 与 `[mid,hi)`；被关闭分片的结束位置即已追加条数，子分片位置从 0 起并以它为父。
- 合并 `Merge(a, b)`：两个首尾相接的开放分片（与参数次序无关）关闭，
  产生并集子分片，子分片以两者为父。
- 分片**已排空**当且仅当已关闭且已提交进度等于结束位置；关闭时无记录者（`end==0`）立即视为已排空。

### 租约规则

- 租约只授予：未排空、无有效持有者、且全部父分片均已排空的分片；
  同一工作者的有效租约数不超过上限。
- 租约自授予或最近一次续租的时钟起有效 `ttl`；时钟 `>=` 到期时刻即失效，失效后他人可领取。
- 提交进度须由有效持有者发出，只增不减且不超过已追加条数。
- 时钟只能单调前进（`AdvanceClock`）。

### 拒绝次序（只报第一个原因，整体拒绝且不改状态）

- 分裂：分片不存在 → 已关闭 → 分裂点越界。
- 合并：分片不存在 → 已关闭 → 不相邻（含两参数相同）。
- 领取：分片不存在 → 已排空 → 有父分片未排空（`RejectError.Parents` 列出全部）→
  已有有效持有者 → 工作者超限。
- 续租与提交：非有效持有者（含已过期）→ 进度回退 → 超过已追加条数。
- 推进时钟回退、追加键越界亦整体拒绝。

所有拒绝以 `*coordinator.RejectError` 返回，携带 `Reason` 与判定依据 `Detail`。

### 本地验证

```bash
# 全部测试（日志打印每个用例的输入、输出与判定依据）
go test -v ./coordinator/

# 竞态检测（并发追加/领取/续租/提交/推进时钟）
go test -race ./coordinator/
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
