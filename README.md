# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## Pod 阶段与重启退避状态机（`pod` 包）

`pod` 包实现了一个带重启预算与活跃期限的 Pod 状态机：管理按序运行的
初始化容器与并行的应用容器，按重启策略与预算决定容器退出后的去向，
并推导 Pod 阶段与每个容器的下次可启动时刻。所有操作可并发调用，结果
等价于某个串行顺序；相同操作序列重放得到完全相同的状态、阶段与退避
时刻。

### 构造参数

| 参数 | 含义 | 取值 |
| --- | --- | --- |
| `InitContainers` (I) | 初始化容器数 | 0..16 |
| `AppContainers` (A) | 应用容器数 | 1..16 |
| `Policy` | 重启策略 | `Always` / `OnFailure` / `Never` |
| `BackoffBase` (B) | 退避基数（毫秒） | 1..1e12 |
| `BackoffMax` (M) | 退避上限（毫秒） | B..1e12 |
| `BackoffReset` (R) | 退避重置时长（毫秒） | 1..1e12 |
| `ActiveDeadline` (D) | 活跃期限（毫秒），0 表示不设 | 0..1e12 |
| `RestartBudget` (X) | 全局重启预算，0 表示不限 | 0..1e6 |

参数越界时 `New` 整体拒绝并返回 `ErrInvalidConfig`。容器编号 0 到
I+A−1，前 I 个为初始化容器。每个容器有状态（`Waiting`/`Running`/
`Succeeded`/`Failed`）、连续失败计数 k、下次可启动时刻 next 与"是否曾
启动过"；Pod 另记全局重启计数 restarts（全部容器共用）与计时起点 t0
（第一次成功的 `Start` 设定，之后不再改变）。

### 重启策略下的退出去向

| 容器类型 | 退出码 | Always | OnFailure | Never |
| --- | --- | --- | --- | --- |
| 初始化 | 0 | Succeeded | Succeeded | Succeeded |
| 初始化 | 非 0 | 重启 | 重启 | Failed |
| 应用 | 0 | 重启 | Succeeded | Succeeded |
| 应用 | 非 0 | 重启 | 重启 | Failed |

### 重启预算

判定为"进入重启"时，若 X>0 且 restarts 已不小于 X，则改按 Never 处理
（退出码 0 置 Succeeded，非 0 置 Failed），且不改变 k、next 与
restarts。restarts 由全部容器共用，X>0 时永不超过 X；X=0 表示不限。

### 退避与重置公式

真正进入重启时（含 Always 下退出码为 0 的情况）：

1. 若本次运行时长 `ran = now − startedAt` 满足 `ran ≥ R`，先把 k 置 0；
2. `delay = min(B·2^k, M)`（乘法不会溢出：仅当 `2^k ≤ M/B` 时才移位，
   否则直接取 M）；
3. `next = now + delay`，随后 k 加 1、restarts 加 1，容器回到 `Waiting`。

### 活跃期限与 Phase 判定次序

`Phase(now)` 按固定次序判定：

1. D>0 且 t0 已设且 `now − t0 ≥ D` → `Failed`；
2. 任一初始化容器 `Failed` → `Failed`；
3. 全部应用容器处于终态：任一 `Failed` → `Failed`，否则 `Succeeded`；
4. 有初始化容器尚非 `Succeeded`，或没有任何应用容器曾启动过 → `Pending`；
5. 否则 → `Running`。

`Reason(c, now)`：容器非终态且 Phase 因期限为 `Failed` 时返回
`DeadlineExceeded`；否则容器 `Waiting` 且 `now < next` 时返回
`CrashLoopBackOff`；其余返回容器状态名。

### 拒绝原因与次序

操作被拒时按以下顺序只报第一个原因（拒绝不改变任何状态）：

1. `InvalidArgument`：容器编号越界或 now < 0；
2. `ClockRegression`：now 小于已被接受的 Start/Exit 见过的最大 now
   （`Phase`/`Reason` 不检查此项）；
3. `NotWaiting` / `NotRunning`：Start 的容器不是 Waiting，Exit 的容器
   不是 Running；
4. `DeadlineExceeded`（仅 Start）：t0 已设、D>0 且 `now − t0 ≥ D`；
5. `InitNotComplete`（仅 Start）：初始化容器门控未通过；
6. `Backoff`（仅 Start）：now 小于该容器的 next。

错误为 `*pod.Error`，携带 `Reason` 字段，并可用 `errors.Is` 匹配
`ErrInvalidArgument` 等哨兵错误。

### 本地验证

```bash
# 全部测试（含 2000 组随机事件序列与朴素模拟的逐步对照）
go test ./pod/

# 查看每个随机事件的输入、输出与判定依据日志
go test -run TestNaiveCrossCheck -v ./pod/

# 竞态检测
go test -race ./pod/
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
