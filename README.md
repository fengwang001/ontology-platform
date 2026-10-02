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

## dnssrv：DNS SRV 加权选择器

`dnssrv` 包提供带 TTL 过期淘汰与指数失败冷却的 RFC 2782 风格 SRV 选择器（
`dnssrv/selector.go`）。线程安全，单一互斥锁保证并发操作等价于某个串行顺序；
`Pick` 不修改任何状态，相同状态与相同 `(r, now)` 结果完全一致，可精确复现。

### 构造与参数范围

- `New(Cap, CoolCap, Wr)`：`Cap >= 1`；`CoolCap`、`Wr` 均在 `[1, 10^9]`。
  非法配置整体拒绝，返回 `ErrInvalidConfig`。
- `Add(target, port, priority, weight, ttl, now)`：`target` 非空，
  `port ∈ [1,65535]`，`priority/weight ∈ [0,65535]`，`ttl ∈ [1,10^9]`，
  `now ∈ [0,10^15]`。
- `Failure(target, port, cooldown, now)`：`cooldown ∈ [1,10^9]`，时间范围同上。
- `Success(target, port, now)`、`Pick(r, now)`、`Purge(now)` 均校验时间范围。

拒绝错误（可用 `errors.Is` 区分，按检查顺序只报第一个）：
`ErrInvalidArg`（参数非法）、`ErrInvalidTime`（时间非法）、`ErrFull`（已满）、
`ErrNotFound`（记录不存在）、`ErrExpired`（记录已到期）、
`ErrNoAvailable`（无可用记录）。被拒绝的操作不改变任何状态。

### 登记序号与重复登记

- 记录标识为 `(target, port)`。新标识首次 `Add` 时分配登记序号，从 1 起严格
  递增；`Purge`/满容量淘汰删除后再次登记获得全新序号。
- 重复登记同一标识是更新：覆盖 `priority`、`weight`，到期时刻改为 `now+ttl`，
  但保留原登记序号、冷却截止时刻、连续失败数 `f` 与上次失败时刻 `lf`，也不占用
  名额、不触发淘汰。

### 组选择与组内排序（RFC 2782）

1. `Pick(r, now)` 先在可用记录中取**最低 priority** 作为当前组；整组不可用时
   降到下一优先级。
2. 组内排序：`weight == 0` 的记录按登记序号升序排在最前，再接 `weight > 0`
   的记录按登记序号升序。
3. 设组内权重和为 `S`，令 `r1 = r mod (S+1)`（`r` 为非负 uint64），按排序顺序
   累计权重，选第一个累计值 `>= r1` 的记录。因此权重 0 的记录只在 `r1 == 0`
   时被选；`r1 == S` 时选最后一个有权重的记录；全组权重为 0 时 `S=0`、
   `r1` 恒为 0，选登记序号最小者。

### 可用性判定

记录在 `now` 可用当且仅当 `now < 到期时刻` 且 `now >= 冷却截止时刻`
（冷却截止初值为 0）。即冷却在截止时刻整点恢复，到期在 `now == expire` 整点
失效。

### 指数失败冷却与静默期

- `Failure`：若 `lf` 为空或 `now >= lf + Wr`，先把 `f` 清零；随后 `f += 1`、
  `lf = now`。有效冷却
  `min(CoolCap, cooldown × 2^min(f-1, 30))`，
  冷却截止时刻取 `max(原值, now + 有效冷却)`——只增不减，后续较小的失败冷却
  不会缩短截止时刻。
- `Success` 只把 `f` 清零，不改冷却截止时刻，也不改 `lf`。
- `Failure`/`Success` 对已到期记录（`now >= expire`）返回 `ErrExpired`。

### 满容量与淘汰

- `Purge(now)` 删除所有 `expire <= now` 的记录并返回删除个数。
- `Add` 新标识时若已存记录数已达 `Cap`：先执行一次等价于 `Purge(now)` 的
  淘汰，淘汰后仍达 `Cap` 才返回 `ErrFull`（此时没有到期记录，故无任何删除）。
  更新已有标识不需要名额，也不触发淘汰。记录数任何时刻不超过 `Cap`。

### 本地验证

```bash
# 全量测试
go test ./dnssrv/

# 竞态检测（并发操作互斥安全）
go test -race ./dnssrv/

# 规则专项用例（详细日志打印输入、输出与判定依据）
go test -run 'TestPick|TestExponent|TestExpiry|TestCapacity|TestPurge|TestRe' -v ./dnssrv/

# 2000 组随机记录集/失败成功序列/随机数 与朴素参考模型逐步对照
go test -run TestDifferentialRandom -v ./dnssrv/
```

测试覆盖：权重 0 仅在 `r1=0` 被选且排在最前、`r1=S` 选最后一个有权重记录、
全 0 权重选登记序最小者、冷却/到期整点边界、重复登记保留序号与冷却状态、
`max` 不缩短截止、CoolCap 封顶与指数位移 30 封顶、静默期 `now == lf+Wr`
整点清零（差 1 不清零）、`Success` 不清冷却不改 `lf`、整组不可用降级、
Purge 后重登记换新序号、满容量更新不淘汰/新标识先淘汰再入库/淘汰后仍满报错、
被拒绝操作快照不变，以及 8 goroutine 并发竞态检测。
