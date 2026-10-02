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

## matchmaking：按等待时间放宽评分窗口的对局匹配大厅

`matchmaking` 包实现了一个并发安全的匹配大厅（`Lobby`）。

构造参数：初始容忍半径 `W0`、每单位时间放宽量 `G`、容忍半径上限 `Wmax`、队列容量 `Cap`。

### 容忍半径公式

玩家在时刻 `now` 的容忍半径（`joined` 为加入时刻）：

```
w = min(Wmax, W0 + G * (now - joined))
```

两名玩家 `a`、`b` 在时刻 `now` 可配对，当且仅当双方互相接受：

```
|ra - rb| <= min(wa, wb)
```

### 撮合规则（Tick）

1. 把队列中全部玩家按 `(joined 升序, id 升序)` 排成处理序。
2. 依次取处理序中仍未配对的玩家 `a`，在其余仍未配对的玩家中找可配对者：
   选 `|ra-rb|` 最小者；并列取 `joined` 较小者；再并列取 `id` 较小者。
3. 找到则二人成对（记录 `A`、`B`、评分差 `Diff` 与撮合时刻 `At`）并离开队列；
   找不到则 `a` 留在队列，继续处理下一位。
4. `Tick` 返回本次产生的配对，按产生次序排列。`Queue()` 按 `(joined, id)` 返回当前队列。

### 错误优先级（只报第一个，被拒绝的操作不改变任何状态）

- 构造：`W0 < 0`；`G < 0` 或 `G > 1e6`；`Wmax < W0`；`W0` 或 `Wmax > 1e9`；`Cap < 2`。
- `Join`：时间非法（`now < 0` 或 `now > 1e9`）→ 时钟回退（`now` 小于已接受操作见过的最大 `now`）
  → `id < 1` → 评分不在 `[0, 5000]` → `id` 曾经出现过 → 队列已满。
- `Leave`：时间非法 → 时钟回退 → `id` 从未出现 → 已配对 → 已离开。
- `Tick`：仅检查时间非法与时钟回退。

每种拒绝原因对应一个可区分的哨兵错误（如 `ErrInvalidTime`、`ErrClockRollback`、
`ErrDuplicateID`、`ErrQueueFull` 等），可用 `errors.Is` 判定。

### 不变量

- 所有方法可并发调用，结果等价于某个串行顺序（内部由互斥锁串行化）。
- 任意玩家同一时刻只处于排队 / 已配对 / 已离开三态之一，且至多属于一对。
- 每一对在其撮合时刻都满足双向接受；队列长度不超过 `Cap`。
- 每次 `Tick` 结束后，留在队列里的任意两名玩家在该 `now` 都不可配对。
- 相同的操作序列重放得到完全相同的配对序列与队列。

### 本地验证

```bash
# 全部测试（含 2000 组随机操作序列与朴素实现对照、并发不变量检查）
go test ./matchmaking/

# 竞态检测
go test -race ./matchmaking/

# 查看随机对照测试打印的输入、输出与判定依据
go test -v -run TestRandomSequencesMatchNaive ./matchmaking/
```
