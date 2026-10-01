# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## Pod 阶段与重启退避状态机（`podstate` 包）

`podstate` 实现了一个可并发调用、可确定性重放的 Pod 状态机：初始化容器按序门控、应用容器并行运行，按重启策略与全局重启预算决定容器退出后的去向，并推导 Pod 阶段与每个容器的下次可启动时刻。

### 构造参数（`podstate.Config`）

- `InitContainers I`：初始化容器数，`0 ≤ I ≤ 16`。
- `AppContainers A`：应用容器数，`1 ≤ A ≤ 16`。
- `RestartPolicy`：`Always` / `OnFailure` / `Never`。
- `BackoffBase B`、`BackoffMax M`：`1 ≤ B ≤ M ≤ 10^12`（毫秒）。
- `BackoffReset R`：`1 ≤ R ≤ 10^12`（毫秒）。
- `ActiveDeadline D`：`0 ≤ D ≤ 10^12`，`0` 表示不设活跃期限。
- `RestartBudget X`：`0 ≤ X ≤ 10^6`，`0` 表示不限重启次数。

容器编号 `0 .. I+A-1`，前 `I` 个为初始化容器。每个容器维护状态（`Waiting/Running/Succeeded/Failed`，初始 `Waiting`）、连续失败计数 `k`（初始 0）、下次可启动时刻 `next`（初始 0）与“是否曾启动”标记；Pod 维护全局重启计数 `restarts`（全部容器共用）、计时起点 `t0`（第一次被接受的 `Start` 的时刻，一经设定不变）与单调时钟最大值 `maxNow`。

### 各重启策略下的退出去向（`Exit(c, code, now)`）

| 容器类型 | 退出码 | Always | OnFailure | Never |
| --- | --- | --- | --- | --- |
| 初始化 | 0 | `Succeeded` | `Succeeded` | `Succeeded` |
| 初始化 | 非 0 | 重启 | 重启 | `Failed` |
| 应用 | 0 | 重启（退避） | `Succeeded` | `Succeeded` |
| 应用 | 非 0 | 重启 | 重启 | `Failed` |

### 重启预算

判定为“进入重启”时，若 `X > 0` 且 `restarts ≥ X`，改按 `Never` 处理：`code == 0` 落为 `Succeeded`，非 0 落为 `Failed`，且**不**改变 `k`、`next`、`restarts`。预算由全部容器共用，`X == 0` 时不限。

### 退避与重置公式

真正进入重启时依次执行：

1. 令本次运行时长 `ran = now - startedAt`；若 `ran ≥ R`，先把该容器的 `k` 置 0（`ran == R` 即重置，`ran == R-1` 不重置）。
2. `delay = min(B·2^k, M)`（逐次翻倍并封顶，任何 `k` 都不溢出）。
3. `next = now + delay`，随后 `k += 1`、`restarts += 1`，状态置回 `Waiting`。

`Always` 下退出码 0 的重启与失败重启走同一规则。`Start` 要求 `now ≥ next`（恰等于 `next` 可启动，差 1 处于退避）。

### 活跃期限与 Phase 判定次序

`Start` 门控顺序：参数合法 → 时钟未回退 → 容器为 `Waiting` → 未超期（`t0` 已设、`D > 0` 且 `now - t0 ≥ D`；`now - t0 == D` 即超期）→ 初始化已完成（初始化容器要求其前面的初始化容器全部 `Succeeded`；应用容器要求全部初始化容器 `Succeeded`）→ `now ≥ next`。超期只拒绝 `Start`，`Exit` 仍可接受。

`Phase(now)` 按以下次序判定（只检查 `now < 0`，不检查时钟回退）：

1. `D > 0`、`t0` 已设且 `now - t0 ≥ D` → `Failed`。
2. 任一初始化容器 `Failed` → `Failed`。
3. 全部应用容器均终态：有任一 `Failed` → `Failed`，否则 → `Succeeded`。
4. 有初始化容器尚非 `Succeeded`，或没有任何应用容器曾启动过 → `Pending`。
5. 否则 → `Running`。

`Reason(c, now)`：容器非终态且因期限为 `Failed` 时返回 `DeadlineExceeded`；否则 `Waiting` 且 `now < next` 返回 `CrashLoopBackOff`；其余返回状态名（`Waiting/Running/Succeeded/Failed`）。

被拒绝的操作不改变任何状态、`k`、`next`、`restarts` 与 `t0`（包括不推进 `maxNow`）。所有方法由互斥锁保护，并发调用等价于某个串行顺序；相同操作序列重放得到完全相同的状态、Phase 与 `next` 序列。

### 本地验证

```bash
# 全量测试
GOCACHE=/tmp/gocache go test ./...

# 竞态检测 + 详细输出（随机对照测试会逐条打印输入、输出与判定依据）
GOCACHE=/tmp/gocache go test -race -v ./podstate

# 只跑 2000 组随机事件序列与朴素模拟的对照测试
GOCACHE=/tmp/gocache go test -run TestRandomDifferential2000 -v ./podstate

# 覆盖率与静态检查
GOCACHE=/tmp/gocache go test -coverprofile=coverage.out ./...
GOCACHE=/tmp/gocache go tool cover -html=coverage.out
GOCACHE=/tmp/gocache go vet ./...
gofmt -l .
```

若环境默认的 Go 构建缓存只读，可按上例用 `GOCACHE=/tmp/gocache` 指定可写缓存目录。

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
