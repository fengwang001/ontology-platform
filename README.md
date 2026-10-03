# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 现货容量市场（`spot` 包）

`spot` 包实现带统一出清价、中断免费小时与整点边界规则的现货容量市场。
小时长固定 3600 秒，所有操作的 `t` 非递减。

### 排名与出清价

- 所有未终止请求（运行中与等待中）按 **(bid 降序, 提交序号升序)** 排名，
  提交序号按成功的 `Request` 递增；并列时先提交者胜出。
- 排名前 K（当前容量）的请求构成运行集合，其余等待。
- 出清价为第 K+1 名的 bid；不足 K+1 个请求时为底价 Pmin。
- 价格历史初值为 `[(0, Pmin)]`，出清价变化时记录 `(t, 价)`，
  同一 t 的多次记录以最后一次为准；`price(x)` 取历史中时刻不大于 x 的最后一条。

### 运行段的开始与结束

- 每个被接受的操作在 t 时刻先应用自身（加入/移除请求或改容量），再重新计算排名。
- 原等待但进入前 K 的请求开始新的运行段；原在运行但跌出前 K 的请求，
  其运行段以「市场中断」结束（请求仍留在集合中，之后回到前 K 会开始新段）。
- 用户 `Terminate` 使运行段以「用户终止」结束；等待中的请求被终止不产生费用。
- 操作返回事件清单：先结束事件再开始事件，各自按 id 升序；结束事件含该段费用。

### 计费口径

- 运行段 `[s, e)` 结束时结算：用户终止收 `ceil((e-s)/3600)` 小时，
  市场中断收 `floor((e-s)/3600)` 小时（不足一小时免费）。
- 第 k 个小时（k 从 0 起）的价为 `price(s + 3600k)`，即该小时起点时刻的价。
- 恰在整点边界结束不多收下一小时；费用使用大整数，保证非负且精确。
- `Bill(id)` 返回该请求全部已结束运行段的费用之和。

### 拒绝规则

- 构造参数越界（K 不在 [1,1000] 或 Pmin 不在 [1,1e9]）以 `ErrInvalidConfig` 整体拒绝。
- `Request` 按序只报第一个：参数非法 → 时钟回退 → id 重复（含已终止的）。
- `Terminate` 按序只报第一个：参数非法 → 时钟回退 → 不存在 → 已终止。
- `SetCapacity` 按序只报第一个：参数非法（k2 越界）→ 时钟回退。
- 被拒绝的操作不改变请求集合、排名、价格历史与最大 t。
- 所有方法可并发调用（内部互斥锁串行化），结果等价于某个串行顺序。

### 本地验证

```bash
# 规则单元测试（含题目示例逐步走查）
go test ./spot/

# 与朴素模拟对照 2000 组随机序列（-v 打印输入、输出与判定依据）
go test -run TestAgainstNaiveSimulation -v ./spot/

# 竞态检测
go test -race ./spot/
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
