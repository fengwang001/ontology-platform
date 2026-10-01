# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 对局匹配大厅

根包提供 `NewLobby(initialRadius, growthPerTime, maxRadius, capacity)` 和 `Lobby`：

- `Join(id, rating, now)`：加入评分在 `0..5000` 的玩家，`joined = now`。
- `Leave(id, now)`：让排队中的玩家离开。
- `Tick(now)`：执行一轮撮合并返回本轮新增配对。
- `Queue()`：返回当前排队玩家的防御性副本，按 `(joined, id)` 排序。

玩家在时刻 `now` 的容忍半径为：

```text
w = min(Wmax, W0 + G × (now - joined))
```

两名玩家只有在

```text
|ratingA - ratingB| <= min(wA, wB)
```

时才双向接受，因此半径较窄的一方决定是否可配对。

### 撮合规则

每轮 `Tick` 先把全部排队玩家按 `(joined 升序, id 升序)` 排成处理序。随后依次取尚未配对的玩家 `A`，在其余尚未配对玩家中枚举可接受对手：

1. 选择评分差最小的 `B`。
2. 评分差并列时，选择 `joined` 较早者。
3. 仍并列时，选择 `id` 较小者。

找到后记录 `A`、`B`、评分差和撮合时刻，双方立即离开队列并进入已配对状态；找不到时 `A` 留在队列。处理下一位时会跳过本轮已经配对者，因此任何玩家至多属于一对。

所有公开方法都由互斥锁保护，并发调用的结果等价于某个合法串行顺序。

### 参数与错误优先级

构造参数在以下情况下返回 `ValidationError`：

- `W0 < 0`：`invalid_w0`
- `G < 0` 或 `G > 10^6`：`invalid_g`
- `Wmax < W0`：`invalid_wmax`
- `W0 > 10^9`：`invalid_w0`
- `Wmax > 10^9`：`invalid_wmax`
- `Cap < 2`：`invalid_capacity`

操作错误只报告优先级最高的第一个：

- `Join`：时间非法 → 时钟回退 → `id < 1` → 评分非法 → id 曾出现 → 队列已满。
- `Leave`：时间非法 → 时钟回退 → id 从未出现 → 已配对 → 已离开。
- `Tick`：时间非法 → 时钟回退。

被拒绝的操作不会修改队列、玩家状态、配对结果或已接受操作见过的最大时间。

`ValidationError.Reason` 暴露稳定的机器可读原因；`RejectReason` 常量包括 `invalid_time`、`clock_went_back`、`invalid_id`、`invalid_rating`、`id_already_exists`、`queue_full`、`id_not_found`、`already_matched`、`already_left` 等。

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

# 查看2000组随机序列的输入、输出、半径与判定依据
go test -v -run TestRandomOperationsAgainstNaiveModel

# 竞态检测
go test -race ./...

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
