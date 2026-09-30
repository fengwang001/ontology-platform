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

## 多维资源成组装箱放置器（`placer` 包）

`placer` 包把一个作业的全部副本按贪心评分逐个放到节点上：**要么全部放下，要么整体不放**。

### 数据模型

- 节点：标识 `ID`、`CPU` 与 `Memory` 两维总量。
- 作业：标识 `ID`、副本数 `Replicas(k)`、每副本 `CPU`/`Memory` 需求（均为正）、每节点同作业副本上限 `MaxPerNode(a)`。

### 放置算法

1. 按副本序号 `0..k-1` 逐个选点；后一个副本看到前一个副本放置后的剩余量。
2. 候选节点须同时满足：剩余 CPU、剩余内存都放得下该副本，且该作业在此节点上的（含本次已试探放置的）副本数小于 `a`。
3. 对每个候选计算放置该副本**之后**两维剩余占比：
   - `score = max(剩余CPU/总CPU, 剩余内存/总内存)`（取较大者，不是求和）。
   - 评分小者优先；占比比较一律用交叉相乘（实现为 `math/big` 精确整数，跨乘积可为 ~1e37），**不使用浮点**。
   - 评分并列时取节点标识升序。
4. 只按此贪心：任一副本没有候选节点，整个作业不可放置，已试探选择的副本全部撤销（逐副本减去占用），即使客观上存在别的可行放法也不尝试。

### 两类不可放置

贪心失败后按下列规则归类（同时成立时报永久）：

- **永久不可放** `ErrPermanentlyInfeasible`：设空载下能容纳单个该副本的节点数为 `n`，当 `n == 0` 或 `k > a×n`。
- **暂时不足** `ErrTemporarilyInsufficient`：其余情形（理论上有解，但当前占用下贪心走到某副本时无候选）。

### 拒绝与错误原因（可区分）

多因并存时只报第一个，顺序为「节点参数 → 作业参数 → 作业重复」：

- 节点：`ErrNodeCapacityNonPositive`（任一维总量非正）→ `ErrDuplicateNodeID`（标识重复）。
- 作业：`ErrReplicasTooSmall`（`k<1`）→ `ErrRequestNonPositive`（副本需求非正）→ `ErrMaxPerNodeTooSmall`（`a<1`）。
- 已放置：`ErrDuplicateJob`；移除未知作业：`ErrJobNotFound`。

任何被拒绝的操作都不会改变节点占用（构造失败返回 nil，放置失败回滚）。

### 移除、查询与并发

- `Remove(jobID)` 释放该作业全部副本，占用回到放置前。
- `JobNodes(jobID)` 返回每个副本所在节点（按副本序号）；`NodeUsage(nodeID)` 返回两维占用；`Snapshot()` 返回确定性的 `节点 -> 作业 -> 副本数`。
- 全部方法在内部 `sync.RWMutex` 下执行，可并发调用，结果等价于某个串行顺序；节点按构造顺序遍历、并列按 ID 排序，因此相同节点与操作序列重放结果完全相同。
- `SetLogger(io.Writer)` 可重定向判定日志（默认 `os.Stderr`，传 `nil` 关闭）：日志含每次操作的输入、逐副本选点与剩余量、最终输出，以及失败时的回滚、`permanent/temporary` 分类依据（`n`、`k`、`a*n`）。

### 本地验证

```bash
# 全部用例（含竞态检测）
go test -race -v ./placer

# 覆盖率
go test -coverprofile=coverage.out ./placer
go tool cover -html=coverage.out

go vet ./...
gofmt -l .
```

关键测试覆盖：评分取较大剩余占比而非求和（`TestScoreUsesMaxRatioNotSum`）、后续副本看到更新后的剩余（`TestLaterReplicasSeeUpdatedRemaining`）、每节点上限 `a`（`TestMaxPerNodeLimit`）、贪心失败成组撤销（`TestGreedyFailureRollsBackAll`）、永久与暂时不足区分（`TestPermanentNoFittingNode`）、移除后可再放（`TestRemoveThenReplace`）、重放确定性（`TestDeterministicReplay`）、并发（`TestConcurrentAccess`）以及日志输入/输出/判定依据（`TestLogsContainInputsOutputsAndReasoning`）。
