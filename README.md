# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## KMS 密钥生命周期管理器（`kms` 包）

`kms` 实现带惰性自动轮换、禁用与计划删除窗口的 KMS 密钥生命周期管理器。
构造参数为保留版本上限 `V`（1..64）、删除窗口 `[Wmin, Wmax]`（秒，
1 ≤ Wmin ≤ Wmax ≤ 1e9）与存活密钥数上限 `Kmax`（1..1e6）；任一越界则
整体拒绝（`ErrInvalidParam`）。所有带 `now` 的操作共用一个全局时钟：
`now` 小于此前被接受操作见过的最大 `now` 即为时钟回退
（`ErrClockRegression`）；`Describe` 同样受检但不推进时钟。

### 补建（catch-up）公式与版本淘汰

每个被接受的、涉及某密钥的操作（`Encrypt`/`Decrypt`/`ReEncrypt`/
`Disable`/`ScheduleDeletion`，以及 `Describe` 的虚拟视图）在生效前先对
该密钥补建，当且仅当它 `Enabled`、`P > 0` 且 `d <= now`：

```
c = floor((now - d) / P) + 1            // 到期次数，可超过 1e13
在时刻 d, d+P, ..., d+(c-1)P 各建一个版本 // 创建时刻取计划时刻而非 now
d = d + c*P
```

版本号从 1 起递增、按完整的 `c` 推进且永不复用；但每次补建只物化最后
`min(c, V)` 个版本（由非导出计数器 `materialized` 记录，单次补建物化数
不超过 `V`，空闲 1e15 秒与短空闲的物化数都受 `V` 约束）。版本数超过
`V` 时每建一个就淘汰当前最小版本号，被淘汰版本的凭据不再可解
（`ErrVersionProblem`/`version retired`）。非 `Enabled` 密钥不补建。

### 状态机与 Enable 对 d 的处理

- `Create` → `Enabled`（版本 1 创建于 `now`，`d = now + P`，世代递增）。
- `Disable`：`Enabled` → `Disabled`（先补建）。
- `Enable`：`Disabled` → `Enabled`；若 `P > 0` 且 `d <= now` 则
  `d = now + P`（期间错过的轮换不补建），`d > now` 则不变。
- `ScheduleDeletion(w)`：`Enabled`（先补建）或 `Disabled` → `Pending`，
  `deleteAt = now + w`，`w` 必须在 `[Wmin, Wmax]` 内。
- `CancelDeletion`：`Pending` → `Disabled`（不是 `Enabled`），`d` 不变。
- `deleteAt <= now` 的密钥在该 `now` 下已删除：对所有操作视同不存在，
  名额立即让出。删除后可同名再建，世代号递增；旧世代凭据报
  `ErrKeyDeleted`，从未创建过的 id 报 `ErrNotFound`。

### 拒绝次序（只报第一个）

参数非法 → 时钟回退 → 不存在 → 密钥已删除 → 状态冲突（带出当前状态）
→ 状态拒绝（`Encrypt`/`Decrypt`/`ReEncrypt` 区分 `disabled` 与
`pending deletion`）→ 版本问题（区分 `version does not exist` 与
`version retired`，按补建后的版本表判定，补建先在副本上计算，任一拒绝
则副本丢弃）→ 超限（`Create` 超过 `Kmax`）。被拒绝的操作不改变任何
密钥、版本表、`d`、`deleteAt`、世代计数与全局时钟，也不触发补建与回收。

### 删除回收与并发

计划删除用最小堆（按 `deleteAt`）惰性回收：仅在被接受的操作生效时弹出
到期项，取消删除使堆条目作废（stale）。每次回收的弹出次数不超过本次
到期删除的密钥数加上作废条目数再加一，与存活密钥总数无关（由非导出
计数器 `heapPops` 记录）。所有操作可并发调用，结果等价于某个串行顺序；
相同操作序列重放得到完全相同的版本号、创建时刻与错误。

### 本地验证

```bash
go test ./kms/                 # 单元测试（规格示例 + 全部边界）
go test -race -v ./kms/        # 竞态检测；随机对照打印输入/输出/判定依据
go test -run TestRandomAgainstNaive -v ./kms/   # 2000 组随机序列 vs 朴素模拟
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
