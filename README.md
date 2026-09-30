# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 分片谱系与租约消费协调器（`lineage` 包）

在数据流分片不断分裂（split）与合并（merge）的过程中协调消费租约，保证：

- 任一分片任一时刻至多一个有效持有者；
- 同一键的记录始终严格按追加顺序被提交消费；
- 追加不丢不重；同一操作序列重放结果完全相同；
- 所有操作可并发调用（内部互斥串行化）。

### 数据模型与谱系

- 键空间为整数 `[0,K)`，初始只有一个开放分片 `[0,K)`，分片 ID 为 0。
- `Append(key)` 将记录路由到当前唯一覆盖 `key` 的开放分片，返回
  `(分片ID, 位置)`，位置在每个分片内从 0 起逐 1 递增。
- `Split(id, mid)` 要求 `lo < mid < hi`，产生子分片 `[lo,mid)` 与
  `[mid,hi)`，ID 依次分配；`Merge(a,b)` 要求两开放分片首尾相接
  （`a.hi == b.lo` 或反之，与参数次序无关；同一分片不合法），
  产生并集子分片，父列表按 ID 升序记录。
- 被分裂/合并的分片立即**关闭**，其**结束位置 = 当时已追加条数**并冻结；
  子分片位置从 0 起，以被关闭分片为父。开放分片始终构成键空间的一个划分。

### 排空判定

分片**已排空**当且仅当：`已关闭 && 已提交进度 == 结束位置`。
关闭时无记录（结束位置 0）立即视为已排空。开放分片即使提交了全部已追加
记录也不算排空（仍可继续接收追加）。

### 租约规则

- `Grant(id, worker)` 仅在下列条件全部满足时成功：分片未排空、当前无有效
  持有者、**全部父分片均已排空**、该工作者当前有效租约数未超过上限
  `maxLeasesPerWorker`。
- 租约自授予（或最近一次 `Renew`）时钟起有效 `ttl`；当
  `时钟 >= 到期时刻` 即失效（即恰在到期时刻失效），之后他人可领取接管。
- `Commit(id, worker, progress)` 仅接受当前有效持有者；进度必须
  `>= 当前已提交`（相等幂等允许）且 `<= 已追加条数`。
- 子分片必须等待全部父分片排空才可被领取，这保证同一键跨分片的提交顺序：
  旧分片上该键的记录未全部提交前，承载该键的新分片无人可消费。

### 拒绝次序（只报第一个原因，整体拒绝、状态不变）

- 追加：键越界（`key ∉ [0,K)`）。
- 推进时钟：时钟回退。
- 分裂：分片不存在 → 已关闭 → 分裂点越界。
- 合并：任一分片不存在 → 任一分片已关闭 → 不相邻（含两参数相同）。
- 领取：不存在 → 已排空 → 存在未排空父分片（`GrantError.UndrainedParents`
  按 ID 升序列出**全部**未排空父分片）→ 已有有效持有者 → 工作者超限。
- 续租 / 提交：非有效持有者（含租约已过期或分片不存在）→ 进度回退 →
  超过已追加条数。

### 用法

```go
import "ontology/lineage"

// K=键空间大小；ttl=租约有效期；maxLeasesPerWorker=单工作者有效租约上限。
c := lineage.New(100, 10, 4, logger) // logger 可为 nil
c.Append(42)                         // {Shard:0 Position:0}, nil
left, right, _ := c.Split(0, 50)
c.Grant(0, "worker-a")               // 分裂后需先排空父分片
c.Commit(0, "worker-a", 1)
c.Grant(left, "worker-b")            // 父排空后子分片可领
c.AdvanceClock(10)                   // 时钟到达 10，ttl=10 的租约恰在此刻失效
```

`Get(id)` / `List()` 查询分片快照（区间、关闭状态、结束位置、已追加/已提交、
是否排空、父分片、当前持有者与租约是否有效）。传入 `Logger` 后每次操作都会
打印输入、输出与判定依据，便于审计与调试。

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

# 仅运行 lineage 包（日志会打印每个操作的输入/输出/判定依据）
go test -race -v ./lineage

# 单个包 / 单个用例
go test ./ontology
go test -run TestLeaseExpiresExactlyAtDeadline ./lineage

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

若默认 `GOCACHE` 位于只读文件系统，可指定可写缓存：

```bash
GOCACHE=/tmp/gocache GOPATH=/tmp/gopath go test -race ./...
```
