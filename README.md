# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 存储配额管理器

配额管理器位于 `quota` 包，使用互斥锁保护全部状态，所有操作和查询都可并发调用，结果等价于某种串行执行顺序。

### 实体与状态

- `NewManager(Gu, Gg)` 创建管理器，`Gu` 为用户宽限时长，`Gg` 为组宽限时长，取值均为 `1..10^9`。
- `AddGroup(g, soft, hard)` 登记组，`AddUser(u, g, soft, hard)` 登记用户；限额必须满足 `0 <= soft <= hard <= 10^15`。
- 用户和组分别维护编号空间，因此题面示例中的用户 `1` 与组 `1` 可以同时存在。裸编号查询 `Usage(id)`、`Grace(id)` 在同号时返回用户；测试内部可直接比较组状态。
- 每次成功分配都会同时增加用户和所属组用量；每次释放都会同时减少二者用量；成功迁组后，源组与目标组用量之和保持不变。

### 统一计时整理

每个被接受的操作结束时，只对本次改变过用量或限额的实体整理一次：

1. 若用量不大于 `soft`，清除宽限起点。
2. 若用量严格大于 `soft` 且没有起点，把起点置为本次操作的 `now`。
3. 若用量严格大于 `soft` 且已有起点，保持原起点不变。

实体宽限已过当且仅当起点非空，并且：

- 用户：`now >= graceStart + Gu`
- 组：`now >= graceStart + Gg`

边界是闭区间：恰好在 `graceStart + 宽限时长` 时宽限已过，早 1 个时间单位时尚未过期。被拒绝的操作不会整理任何实体，也不会更新已接受的最大 `now`。

### 检查顺序与拒绝原因

所有错误按以下优先级只返回第一个：

1. 参数非法。
2. 时钟回退：`now` 小于已接受操作的最大 `now`。
3. 实体不存在；登记时编号已存在返回实体已存在。
4. 状态错误：`Free` 超释放，或 `Move` 的目标组就是当前组。
5. 用户硬限。
6. 用户宽限已过。
7. 组硬限。
8. 组宽限已过。

`Alloc` 的检查顺序固定为用户硬限、用户宽限、组硬限、组宽限；全部通过后才更新用量和时间。

`SetLimits(kind, id, soft, hard, now)` 可把硬限调到低于当前用量。此时实体已经超过硬限，后续分配会先被硬限拒绝；当前操作本身仍被接受，并按新旧限额整理宽限起点。

`Move(u, g2, now)` 不改变用户自身用量和用户宽限。目标组按用户当前用量 `U` 执行一次等效分配：先检查目标组硬限，再仅当 `U > 0` 时检查目标组宽限；源组执行等效释放。成功后用户改属目标组，并分别整理源组与目标组。

### 可复现验证

确定性场景测试覆盖软硬限边界、宽限前后一刻、宽限过期后只允许释放、重新计时、拒绝原子性、`SetLimits`、`Move` 和并发不变量。

`TestRandomDifferential` 固定随机种子，重放 2000 组随机操作序列，并与按题面规则逐步编写的朴素模拟逐步比较错误、限额、用量、宽限起点和最大时钟。测试日志使用 `-v` 时打印输入、输出和判定原因。

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

# 仅查看配额管理器随机差分日志
go test -race -v ./quota -run TestRandomDifferential -count=1

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
