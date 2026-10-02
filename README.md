# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 影厅选座登记器（`cinema` 包）

`cinema` 实现带保留时限与孤座回避的影厅选座登记器：影厅有 R 排（1..R）、
每排 W 座（1..W）、保留时限 T。`Hold(k, now)` 为 k 人在同一排保留 k 个连续
空闲座，`Confirm(id, now)` 把活跃保留转为永久占用，`Release(id, now)` 立即
释放，`Seats(now)` 查询每个座位在 now 视角下的状态（空闲/保留/已确认）。

### 座位空闲与保留过期口径

- 座位在时刻 now 空闲，当且仅当没有已确认订单占用、也没有活跃保留占用。
- 保留成功时 `expiry = now + T`；`now >= expiry` 即过期，不再占座，且自
  expiry 起可被后续 `Hold` 再选中（过期恰好发生在 expiry 那一刻）。
- 保留/确认/释放是按时间戳记录的事件：`Seats(now)` 是纯函数，只按传入的
  now 计算——创建时刻之前的保留不占座，确认前的时间段显示为保留，释放
 时刻起恢复空闲。`Seats` 不检查也不更新已见最大 now，查询不改变任何状态。
- `Hold`/`Confirm`/`Release` 先检查时钟：now 小于已被接受操作见过的最大
  now 报时钟回退；被拒绝的操作不改变座位、保留与已见最大 now。

### 孤座定义

选定连续段 `[s, s+k-1]` 后，若其左侧紧邻的最大空闲连续区长度恰为 1，
或右侧同理，则该候选带「孤座」。长度为 0（紧邻墙壁或占用座）或不小于 2
都不算孤座；紧邻墙壁的单个空闲座也算孤座（墙壁侧长度按实际空闲区计，
不因墙壁而归零）。

### 两遍选座与排序规则

1. 候选为所有排中全部空闲的连续段 `[s, s+k-1]`（不跨排）。
2. 第一遍只在全部排的无孤座候选中选；一个都没有时才退到第二遍，在全部
   候选中选（因此后排的无孤座候选胜过前排的有孤座候选）。
3. 选择次序：排号小者优先；段中心偏离 `|(2s+k-1)-(W+1)|` 小者优先；
   起始座号 s 小者优先。

保留号从 1 起严格自增、无空洞，只有成功的 `Hold` 才消耗号码。`Confirm`/
`Release` 的拒绝原因按「保留号从未发出 → 已确认 → 已释放 → 已过期」的
顺序判定（已确认且已过期报已确认，已释放且已过期报已释放）。所有操作
可并发调用，结果等价于某个串行顺序；相同操作序列重放得到完全相同的
选座结果。

### 本地验证

```bash
# 全部单测（孤座两侧与墙壁、两遍选座、dev 并列取小 s、k==W、
# expiry 边界、释放立即可选、已确认不可释放、拒绝不改状态等）
go test ./cinema/

# 查看每个操作的输入、输出与判定依据日志
go test -v ./cinema/

# 2000 组随机操作序列与朴素实现对照 + 并发不变量（竞态检测）
go test -race ./cinema/
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
