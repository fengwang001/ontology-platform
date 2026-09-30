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

## 登录失败限制器（`loginlimit` 包）

`loginlimit.Limiter` 按 **账号** 与 **来源** 两个独立维度统计登录失败，
并对暴力尝试实施渐进式锁定。创建参数：

- `Window`（W）：失败计数的滑动窗口长度
- `Threshold`（K）：窗口内触发一次锁定所需的失败数
- `BaseLock`（B）：首次锁定时长
- `MaxLock`（M）：单次锁定时长上限，要求 `M >= B`
- `Cooldown`（R）：级别冷却时长

创建时任一参数非正，或 `M < B`，返回 `*loginlimit.ConfigError`（原因
`invalid_config`）。

### 键状态与失败记录

- 每个账号与每个来源各为一个独立键，内部记录：窗口内失败时刻列表、
  锁定截止时刻 `lockUntil`、已触发级别 `j`。
- 每次尝试前先清理窗口外记录：只保留严格晚于 `当前时刻 - W` 的失败时刻。
- `Attempt(account, source, at, passwordOK)` 的时钟 `at` 必须单调不减；
  限流器内部记录已见到的最大读数。

### 尝试判定顺序

1. 参数校验（不改变任何键状态，按顺序只报第一个原因）：
   - 账号为空 → `empty_account`
   - 来源为空 → `empty_source`
   - `at` 早于已见最大读数 → `clock_backwards`
2. 任一键处于锁定中（`at < lockUntil`）→ 拒绝为 `locked`：
   - 不计失败、不延长锁定；
   - `Result.LockedDimensions` 给出处于锁定的维度（`account`/`source`）；
   - `Result.UnlockAt` 取各被锁维度 `lockUntil` 中**较晚**的一个。
3. 未锁定且口令正确 → 放行：只清空**账号键**的失败记录与级别 `j`
   （来源键不动；`lockUntil` 保留，供冷却判定使用）。
4. 未锁定且口令错误 → 给两个键各记一次失败，返回 `bad_password`。

### 渐进式锁定与冷却

- 口令错误时，任一键窗口内失败数达到 K 即由这第 K 次失败触发锁定：
  - 若 `当前时刻 >= 该键上次 lockUntil + R`，先令 `j = 0`；
  - 然后 `j += 1`，锁定到 `当前时刻 + min(B * 2^(j-1), M)`；
  - 随即清空该键的失败记录。
- 时长逐级翻倍：`B, 2B, 4B, ...`，封顶 `M`，因此合法用户不会被无限期锁住。
- 触发锁定的那一次尝试仍只报 `bad_password`，从下一次尝试起才表现为 `locked`。
- 典型防护：同一来源对多账号喷洒时，账号键各自只有 1 次失败，
  来源键却会达到 K 而被锁定。

### 并发与确定性

- 全部状态变更在同一把互斥锁内完成，`Attempt` 可被并发调用。
- 并发失败不丢失，每次触发级别只加一；锁定期内的并发尝试全部被拒且不改状态。
- 相同的尝试与时钟序列得到相同结果。

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology

# 登录失败限制器（竞态检测 + 详细日志，日志含每次尝试的输入、输出与判定依据）
go test -race -v ./loginlimit

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
