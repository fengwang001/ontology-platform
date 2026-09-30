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

## 令牌验证密钥轮换调度器（`keyscheduler`）

`keyscheduler` 包按「发布 → 启用 → 停签 → 退役」四步推进签名密钥，保证任何在有效期内签发的令牌，在任意验证方缓存状态下都能验证；已退役密钥不再被任何合法令牌需要。

参数（时间均为同一单调时钟下的整数时刻）：

- `C`：验证方缓存最长保留时间（`cacheTTL`，必须为正）。
- `T`：令牌寿命（`tokenTTL`，必须为正）。
- `S`：验证方接受的时钟偏差（`skew`，非负）。

### 四步状态与最早时刻

1. **已发布（Published）**：在时刻 `t` 调用 `Rotate(newID, t)` 后，新密钥立即发布，只可验证、不可签发。启用时刻恒为 `t + C`——这 `C` 的提前量保证时钟最慢、缓存最旧的验证方也一定能在新密钥签发令牌前缓存到它。
2. **活跃（Active）**：到达启用时刻后，新密钥成为唯一活跃密钥（唯一可签发者），旧活跃者同时停签。状态随注入时钟惰性推进：`Rotate`、`Retire`、`Advance`、`Sign`、`VerificationSet`、`Snapshot` 每个操作都会先完成所有已到点的启用。
3. **停签（SignOff）**：只验证、不签发。停签时刻记为**启用时刻**，而不是该启用被实际观察到的时刻，因此久无操作后再观察不会改变后续时刻。最早退役时刻为：

   ```
   RetiresAt = SignOffAt + T + S = (t + C) + T + S
   ```

   `T` 覆盖停签前最后一刻（即启用时刻）签发的令牌的寿命；`S` 覆盖验证方的时钟偏差。令牌在其签发时刻起到 `签发时刻 + T + S` 之前始终在验证集合中。
4. **已退役（Retired）**：`Retire(id, now)` 在 `now >= RetiresAt` 时成功（恰到点即可），密钥立即从验证集合移除。

验证集合 = 已发布 ∪ 活跃 ∪ 停签；任意时刻至多一把活跃密钥且它必在验证集合中。初始密钥在 `New` 时直接活跃。

### 拒绝规则（按顺序只报第一个原因）

- `New`：`C <= 0`、`T <= 0` 或 `S < 0` → `ErrInvalidParam`。
- 任何带时钟的操作：`now` 早于已见最大读数 → `ErrClockMovedBackwards`。
- `Rotate`：先惰性推进，再检查新标识已存在（`ErrKeyIDExists`），再检查已有待启用的新密钥（`ErrPendingActivation`）。
- `Retire`：先惰性推进，再依次检查不存在（`ErrKeyNotFound`）、状态不是停签（`ErrNotSignOff`）、未到最早退役时刻（`*EarlyRetirementError{Earliest}`，携带可退役时刻）。
- 被拒绝的操作不改变任何自身状态（惰性启用由时钟决定，不属于被拒绝操作的改动）。

### 标识为何不可复用

已退役密钥仍保留在内部注册表中，标识永远不能再次用于轮换。复用语义上属于不同代际的密钥会让旧令牌（仍可能在验证方缓存中、处于 `T+S` 宽限期内）被新密钥错误验证，破坏「密钥与签发代际一一对应」的安全假设；不复用使每把密钥的历史可唯一追溯，也让 `Retire` 的状态判定无歧义。

### 并发与确定性

所有操作经同一把互斥锁串行化，`Rotate`、`Retire`、`Advance`、`Sign`、`VerificationSet`、`Snapshot`、`Key`、`ActiveID` 可并发调用，任意交错下上述不变量成立；除时钟外无其他不确定输入，相同的操作与时钟序列在独立实例上重放得到相同状态。操作日志通过 `Logger` 打印每次的输入、输出与判定依据（传 `nil` 默认输出到标准输出）。

### 本地验证

```bash
# 详细日志（每个操作打印输入、输出、判定依据）
go test -v ./keyscheduler

# 竞态检测（并发交错不变量）
go test -race -count=10 ./keyscheduler

# 全量测试 / 覆盖率
go test ./...
go test -coverprofile=coverage.out ./keyscheduler
```

关键用例：`TestRotateActivatesExactlyCAfterRequest`（启用比请求晚 C、恰到点）、`TestRetirementTimeComputedFromActivationEvenIfObservedLate`（久无操作后最早退役时刻仍按启用时刻）、`TestOverlappingRotationsYieldThreeKeys`（连续轮换重叠出三把密钥）、`TestRetireRejectionOrdering` 与退役边界（差一刻返回 `EarlyRetirementError`、恰到点可退）、`TestRotateRejectionsDoNotChangeState`（被拒绝不改状态、退役标识不可复用）、`TestSingleActiveAndTokenLivenessInvariant`（单活跃与令牌存活期不变量）、`TestConcurrentOperations`（并发竞态）、`TestDeterministicReplay`（确定性重放）。
