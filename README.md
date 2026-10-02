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

## srvpick：DNS SRV 加权选择器

`srvpick` 包实现带 TTL 过期淘汰与指数失败冷却的 DNS SRV 记录加权选择器，
按 RFC 2782 的规则先选最低优先级组、再按权重选记录。全部方法并发安全，
效果等价于某个串行顺序；`Pick` 不修改任何状态，同一状态、同一 `(r, now)`
必得同一记录。

### 构造

`New(Cap, CoolCap, Wr)`：`Cap >= 1`，`CoolCap`、`Wr` 均在 `[1, 1e9]`，
否则整体拒绝并返回 `ErrInvalidConfig`。

### 记录与登记

- 记录由 `Add(target, port, priority, weight, ttl, now)` 登记，标识为
  `(target, port)`；首次登记分配从 1 起严格递增的登记序号。
- 重复登记同一标识视为更新：覆盖 `priority`、`weight`，到期时刻改为
  `now+ttl`，保留原登记序号、冷却截止时刻、连续失败数 `f` 与上次失败
  时刻 `lf`。
- 可用性：记录在 `now` 可用当且仅当 `now < 到期时刻` 且
  `now >= 冷却截止时刻`（初值 0）。

### 组选择与组内排序（Pick）

- 取存在可用记录的最小 `priority` 作为当前组；组内全不可用时自然降到
  下一优先级。
- 组内排序：权重为 0 的记录按登记序号升序排在最前，再接权重大于 0 的
  记录按登记序号升序。
- 设组内权重和为 `S`，令 `r1 = r mod (S+1)`（`r` 为非负 uint64），按排好
  的顺序累计权重，选第一个累计值 `>= r1` 的记录。因此权重 0 的记录只在
  `r1 == 0` 时被选中，`r1 == S` 时选中最后一个有权重记录；组内全为 0 权
  重时 `S == 0`，恒选登记序最小者。

### 指数冷却与静默期

- `Failure(target, port, cooldown, now)`：若 `lf` 为空或
  `now >= lf + Wr`（静默期已过，含恰等），先把 `f` 清零；然后 `f` 加一、
  `lf = now`，有效冷却为 `min(CoolCap, cooldown * 2^min(f-1, 30))`，冷却
  截止时刻取 `max(原值, now + 有效冷却)`——只增不减，后续较小冷却不会
  缩短。
- `Success(target, port, now)` 只把 `f` 清零，不改冷却截止时刻与 `lf`。

### 满容量淘汰

- `Add` 遇到已存记录数达 `Cap` 且标识是新的：先淘汰全部到期时刻
  `<= now` 的记录（相当于一次 `Purge`），仍达 `Cap` 才报 `ErrFull`；
  更新已有标识不需要名额也不触发淘汰。任何时刻记录数不超过 `Cap`。
- `Purge(now)` 删除到期时刻 `<= now` 的记录并返回删除个数；被删除的标识
  再次登记获得新的登记序号。

### 拒绝顺序

被拒绝的操作不改变任何记录。各操作按顺序只报第一个错误：

- `Add`：`ErrInvalidParam`（target 空、port 不在 1..65535、priority/weight
  不在 0..65535、ttl 不在 1..1e9）→ `ErrInvalidTime`（now 不在
  0..1e15）→ `ErrFull`。
- `Failure`：`ErrInvalidParam`（target 空、cooldown 不在 1..1e9）→
  `ErrInvalidTime` → `ErrNotFound` → `ErrExpired`。
- `Success`：`ErrInvalidParam`（target 空）→ `ErrInvalidTime` →
  `ErrNotFound` → `ErrExpired`。
- `Pick` / `Purge`：先查 `ErrInvalidTime`；`Pick` 无可用记录时报
  `ErrNoAvailable`。

### 本地验证

```bash
# 全部单元测试（含与朴素参照模型的 2000 组随机对照、重放确定性）
go test ./srvpick/

# 打印随机对照的输入、输出与判定依据（组别、S、r1、累计轨迹）
go test ./srvpick/ -run TestRandomizedCompareWithNaive -v

# 竞态检测（含并发烟雾测试）
go test -race ./srvpick/
```
