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

## redlock：带注入时钟的多数派租约锁获取器

`redlock` 包实现了一个 Redlock 式的多数派租约锁引擎：向 N 个相互独立的节点依次请求同一资源的租约，按往返耗时与时钟漂移推算有效时长。所有时间均由调用方注入，不依赖真实时钟，因此获取成败、失败后的全量释放与重启节点的静默期都可以精确复现。

### 构造

```go
e, err := redlock.New(N, Tn, D, MaxTTL)
```

- `N`：节点数，1 到 9；多数派为 `⌊N/2⌋+1`。
- `Tn`：单节点超时，1 到 10^6。
- `D`：时钟漂移千分数，0 到 1000。
- `MaxTTL`：最大租期，1 到 10^9。

任一参数越界则整体拒绝（`ErrInvalidConfig`）。每个节点保存「资源名 → (令牌, 到期时刻)」与静默截止 `q`（初值 0）；引擎维护时钟水位 `T`（初值 0）与从 1 起编号的令牌。

### Acquire(res, ttl, start, rtt)：时钟推进与到达时刻

被接受的调用（无论成败）领取下一个令牌 `k`。客户端时钟 `c` 初为 `start`，按节点 0 到 N-1 依次处理：

- `rtt[i] == -1`：节点不可达，不执行，`c += Tn`。
- 否则请求在 `a = c + ⌊rtt[i]/2⌋` 到达并执行；随后 `rtt[i] ≤ Tn` 时 `c += rtt[i]`，`rtt[i] > Tn`（超时）时 `c += Tn`。

节点执行规则：`a < q` 拒绝；已存该资源且到期时刻 `> a` 拒绝（NX，同一客户端的旧令牌同样拒绝）；否则存入 `(k, a+ttl)`（覆盖已过期记录）算作授予。只有 `rtt[i] ≤ Tn` 且被授予的节点计入有效授予数 `g`（超时节点即使已授予也不计入）。

处理完后记 `cend = c`，并计算：

```
dr    = ⌊ttl × D / 1000⌋ + 2        （漂移，向下取整后再加 2）
v     = ttl - (cend - start) - dr   （有效时长）
Until = start + ttl - dr            （恒等于 cend + v）
```

当 `g ≥ ⌊N/2⌋+1` 且 `v > 0` 时成功；否则失败，并在 `cend` 向全部 N 个节点（含不可达与超时的）发释放：节点若存该资源且令牌等于 `k` 就删除（不看是否到期）。无论成败 `T` 置为 `cend`，返回令牌、成败、`g`、`v`、`Until`、`cend`。获取失败是正常返回而不是拒绝：它照常消耗令牌、推进 `T` 并执行授予与释放。

### Unlock / Restart / Count

- `Unlock(res, k, now)`：删除全部存该资源且令牌等于 `k` 的节点记录（不看是否到期），返回删除个数，`T` 置为 `now`。
- `Restart(i, now)`：清空节点 `i` 的全部记录并令 `q = now + MaxTTL`（到达时刻小于 `q` 一律拒绝，恰等于 `q` 才可授予），`T` 置为 `now`。静默期保证重启节点不会在旧租约仍可能有效时重新发锁，从而保证同一资源任意两次成功获取的有效区间互不相交：后者的 `cend ≥ min(前者 Until, 前者被 Unlock 的 now)`。
- `Count(res, k, now)`：只读，返回存该资源、令牌为 `k` 且到期时刻严格大于 `now` 的节点数，不改变 `T`。

### 拒绝规则

`now` 与 `start` 的合法范围均为 0 到 10^15。每个操作按固定顺序只报第一个错误：

1. 参数非法（`ErrInvalidArgument`）：资源名为空、`ttl` 越界、`rtt` 长度或取值非法、令牌小于 1、节点下标越界；
2. 时间非法（`ErrInvalidTime`）：`start`/`now` 超出 [0, 10^15]；
3. 时钟回退（`ErrClockRewind`）：`start`/`now` 小于 `T`。

被拒绝的操作不改变任何节点记录、静默截止、`T` 与令牌计数。

### 并发与确定性

所有方法可并发调用，内部以互斥锁串行化，结果等价于某个串行顺序；任意时刻每个节点对每个资源至多一条记录。相同的操作序列重放得到完全相同的令牌、有效授予数与节点记录。

### 本地验证

```bash
# 全部测试（含 2000 组随机序列与朴素模拟对照、有效区间不相交断言）
go test ./redlock

# 竞态检测
go test -race ./redlock

# 查看随机对照日志（输入、输出与判定依据）
go test -v -run TestRandomAgainstNaive ./redlock
```
