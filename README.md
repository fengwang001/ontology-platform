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

## 值班轮值表（`duty` 包）

`duty` 包实现带临时覆盖的值班轮值：`duty.New(members, T0, L)` 构造，
`Add`/`Remove` 管理覆盖，`Who(t)` 查询单点，`Timeline(a,b)` 返回合并后的时间线。

### 轮值与向前外推

- 时刻 `t`（分钟，`int64`）的轮值成员为
  `members[floorMod(floorDiv(t−T0, L), n)]`，其中 `floorDiv` 向下取整、
  `floorMod` 结果非负。
- 因此规则对 `t < T0` 同样适用：`T0−1` 恰好属于最后一个成员；
  `T0−L` 满足 `floorDiv = −1`，按非负取模回绕（`n=3` 时为最后一个成员）。
- 班次区间为 `[T0+kL, T0+(k+1)L)`，左闭右开。

### 覆盖优先级

- 覆盖为 `(id, 成员, [s,e))`，可相互重叠、也可与轮值重叠。
- 任一时刻，所有包含该时刻且未删除的覆盖中，**最后添加**者生效；
  无覆盖时回落到轮值（来源 `"rotation"`）。
- 删除后被覆盖的时段自动恢复为更早的生效覆盖或轮值；已删除的 `id`
  可再次添加，视为全新的最后添加者。
- `Add`/`Remove`/`Who`/`Timeline` 内部以读写锁串行化快照，并发调用
  等价于某个串行顺序，查询不会读到中间态。

### 时间线合并规则

- `Timeline(a,b)` 返回覆盖 `[a,b)` 的最大连续段 `(起,止,成员,来源)`，
  各段首尾相接，且与任意时刻的 `Who` 完全一致。
- 相邻两段仅在**成员与来源都相同**时合并：
  - 单人名册的所有轮值班次合并为一段；
  - 同成员、不同覆盖 `id` 不合并；
  - 覆盖与轮值即使成员相同也不合并。
- 合并后段数超过 `10000` 时整体拒绝（`ErrTimelineTooLarge`），无副作用。

### 拒绝原因（可用 `errors.Is` 区分）

- 构造：`ErrEmptyRoster`、`ErrDuplicateMember`、`ErrInvalidLength`。
- 添加覆盖检查顺序固定：区间（`ErrEmptyInterval` 先于
  `ErrInvertedInterval`）→ 成员（`ErrUnknownMember`）→
  id 重复（`ErrDuplicateOverride`）。
- 删除：`ErrOverrideNotFound`；`Timeline`：区间为空/颠倒、
  `ErrTimelineTooLarge`。被拒绝的操作不改变覆盖集合。

### 本地验证

```bash
# 全量测试（测试日志打印每个输入、输出与判定依据）
go test -v ./duty

# 竞态检测 + 朴素逐分钟实现对照
go test -race -v ./duty

go vet ./...
gofmt -l .
```

测试包含：`t = T0`、`T0−1`、`T0−L`，班次/覆盖边界重合，三个覆盖相互
嵌套与交叉，删除后让位给更早添加者，单人名册合并，同成员不同 id 不
合并，覆盖结束后恢复轮值，时间线 10000 段边界，校验顺序，操作序列重放
确定性，以及并发读写；每个区间用例都与逐分钟扫描的朴素实现逐段对照。
