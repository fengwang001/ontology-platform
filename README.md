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

## 滚动更新步进计划器（`rollout` 包）

`rollout.Planner` 按期望副本数、最大超量比例与最大不可用比例，逐步把旧版本实例
替换为新版本实例，并带最小就绪时长（稳定性）与停滞判定。

### 上限取整（全部整数运算）

- 超量上限：`S = ceil(N*PS/100) = (N*PS + 99) / 100`
- 不可用上限：`U = floor(N*PU/100) = N*PU / 100`
- 若 `S == 0 && U == 0`，则把 `U` 改为 `1`，避免零超量且零不可用时更新无法启动
- 边界示例：`N=10,25% -> S=3,U=2`；`N=1,50% -> S=1,U=0`；`N=3,33% -> S=1,U=0`

### 实例计数、稳定批与可用数

- `a` 旧版本就绪，`b` 旧版本未就绪，`d` 新版本未就绪，`c` 新版本就绪
- `NewReady(k, now)` 记为 `readyAt=now` 的一批；相同 `readyAt` 合并为一批；各批
  之和恒等于 `c`
- 一批在 `now` 稳定当且仅当 `now - readyAt >= MR`（恰好等于即稳定，差 1 不稳定）；
  `cs(now)` 为所有稳定批之和；`MR=0` 时 `cs=c`
- 可用数 `A(now) = a + cs(now)`：新版本“就绪但未稳定”的实例不计可用
- `Done()` 当且仅当 `a+b==0`、`c==N` 且以最近一次被接受操作的 `now` 计 `cs==N`

### `Step(now)` 的计算次序

三个量都基于**调用开始时**的状态一次算出，然后同时生效，`A` 同样取开始时的值：

1. 新建 `up = max(0, min(N+S-(a+b+c+d), N-(c+d)))`，`d += up`
2. 清理 `r1 = b`（旧版本未就绪全部删除，**不占用不可用额度**），`b -= r1`
3. 缩减 `r2 = min(a, max(0, A(now)-(N-U)))`，`a -= r2`
4. 停滞计数：`Done` 成立清零；否则 `up+r1+r2>0` 清零，否则加 1
5. 返回 `(up, r1, r2, stalled)`，其中 `stalled` 当且仅当停滞计数 `>= P`

因此同一步的 `up` 不会因为本步 `r1` 清理出的名额而变大。`NewReady` 清零停滞计数；
`NewFail(k, now)` 按 `readyAt` 大者先扣批（`c` 减、`d` 增），`OldUnready` 把
`a` 转成 `b`；后两者都不改变停滞计数。

### 拒绝规则（按此顺序只报第一个，被拒绝操作不改任何状态）

1. 参数非法：`k < 1` 或 `now < 0`（`Step` 只查 `now`）
2. 时钟回退：`now` 小于已接受的四类操作见过的最大 `now`
3. 超出范围（三种可区分错误）：
   - `NewReady` 的 `k > d`：`ErrNoUnreadyNew`
   - `NewFail` 的 `k > c`：`ErrNoReadyNew`
   - `OldUnready` 的 `k > a`：`ErrNoReadyOld`

构造参数越界返回 `ErrInvalidConfig`；查询（`Done`、`Snapshot`）不会被拒绝。
所有方法可并发调用，互斥临界区保证结果等价于某个全序串行执行。

### 本地验证

```bash
# 全量测试（含 2000 组随机事件与朴素模拟对照）
go test -v ./rollout/

# 并发可串行化与数据竞争检测
go test -race -v ./rollout/

# 随机对照日志（输入、输出、判定依据由测试 t.Logf 打印）
go test -run TestRandomAgainstNaive2000 -v ./rollout/

go vet ./...
gofmt -l .
```
