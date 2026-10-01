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

## 渐进发布控制器（`rollout` 包）

`rollout.Controller` 按阶梯百分比与阶段保持时长推进发布，支持暂停、恢复与回滚；用户是否落入发布范围由确定性分桶决定。所有方法可并发调用，结果等价于某个串行顺序；相同操作序列重放得到完全相同的转移与范围判定。

### 构造与校验

`rollout.New(salt, steps)`：`salt` 为非空字符串；`steps` 至少 2 级，每级为 `{Percent, Hold}`（百分比 1~100 的整数且严格递增，Hold 毫秒不小于 0；最后一级的 Hold 被忽略但仍须非负）。校验顺序为：空盐 → 级数不足 → 百分比越界 → 不严格递增 → Hold 为负，只报第一个错误（`ErrEmptySalt`、`ErrTooFewSteps`、`ErrPercentOutOfRange`、`ErrPercentNotIncreasing`、`ErrNegativeHold`）。

### 阶段推进

- 状态机：`Idle → Running ⇄ Paused → Completed`，任意非终态可 `Rollback` 到 `RolledBack`，`RolledBack` 可重新 `Start`。
- 第 i 级（非最后一级）的到期时刻：`due_i = enteredAt_i + Hold_i + paused_i`，其中 `paused_i` 为该级累计暂停时长。
- `Tick(now)`：仅 `Running` 时，当 `now ≥ due` 即进入下一级；**转移时刻取 `due` 而非 `now`**，下一级的 `enteredAt` 取该 `due`、`paused` 归零，一次 `Tick` 可连续跨越多级；进入最后一级即 `Completed`。其他状态下 `Tick` 无转移（但仍参与时钟记账）。返回值为转移列表 `{From, To, At}`。
- `Pause(now)`/`Resume(now)`：只记录暂停起点 / 累加暂停时长，不推进阶段。
- `Status()` 返回状态、级别（`Idle`/`RolledBack` 为 -1）与当前百分比（`Idle`/`RolledBack` 为 0，`Paused` 沿用当前级百分比）。

### 确定性分桶

对字节序列 `salt + "\x00" + user` 做 32 位 FNV-1a（初值 `2166136261`，先异或字节再乘 `16777619`，对 2^32 取模），哈希对 `10000` 取余得 `b`，当且仅当 `b < 当前百分比 × 100` 时在范围内。百分比只随阶段升高而增大，因此任一用户的范围判定只增不减。

### 错误优先级

所有带 `now` 的操作**先检查时钟回拨**（`now` 小于此前任一次已接受操作的最大 `now`，含无转移的 `Tick`，报 `ErrClockBackwards`），**再检查状态**（报 `ErrInvalidState`）。`InRollout` 空用户报 `ErrEmptyUser`。被拒绝的操作不改变状态、累计暂停时长与最大 `now`。

### 本地验证

```bash
# 全部测试（含 2000 组随机操作序列与朴素模拟器对拍，-v 可见每步输入/输出/判定依据）
go test ./rollout/
go test -race -v ./rollout/

# 单个用例
go test -run TestSpecExample ./rollout/
go test -run TestDifferentialRandom -v ./rollout/
```
