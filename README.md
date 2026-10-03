# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 周期定时器唤醒合并器

实现在 `ontology/coalescer.go`，入口类型为 `Coalescer`。

- 构造：`NewCoalescer(g, B)`，`g` 是两次实际唤醒的最小间隔（`0..10^9`），`B` 是单次唤醒最多触发的定时器数（`1..64`）。
- 定时器：`Add(id, period, slack, next)`，容忍窗口为 `[next, next+slack]`，要求编号非空且不超过 32 字节、`1 <= period <= 10^9`、`0 <= slack < period`、`0 <= next <= 10^15`。
- 唤醒时刻：`Next()` 返回 `w=minTimer(next+slack)`；若已经有上一次唤醒，则返回 `max(minTimer(next+slack), last+g)`。第一次唤醒不受最小间隔限制。
- 候选与批量上限：`Wake()` 在 `w` 处选择所有 `next <= w` 的定时器，按 `(next+slack, 编号字节序)` 升序，只触发前 `B` 个；其余候选保留到下一次唤醒，并立即从后续 `e` 中反映其仍未触发的窗口。
- 延迟与追赶：第 `i` 个触发事件的延迟为 `late=max(0,w-(next+slack))`，跳过数为 `k=floor((w-next)/period)`。这表示 `next+period,...,next+k*period` 均不晚于 `w`，这些名义时刻既不单独触发也不单独计延迟；触发后该定时器的名义时刻推进到 `next+(k+1)*period`，始终严格大于本次 `w`。
- 批量推进：`AdvanceTo(t)` 重复执行满足 `Next() <= t` 的 `Wake()`，返回途中所有唤醒结果；单次调用最多执行 `10^5` 次唤醒，达到上限即停止且不报错。目标时刻之前没有唤醒时，时钟保持不变。
- 统计：`Stats()` 返回累计实际唤醒数、触发事件数、`late > 0` 的触发次数，以及所有触发事件的 `k` 之和。
- 错误优先级：`Add` 依次为参数非法、编号重复、`next < last` 时钟回退、容量已满；`Remove` 依次为参数非法、编号不存在；`AdvanceTo` 依次为 `t` 越界、`t < last`、无定时器。被拒绝的操作不改变定时器、时钟或统计。
- 并发：公开方法使用同一把锁串行化状态变更与查询，语义等价于某个合法的串行交错。

数据结构维护：

- 以 `next+slack` 为键维护 deadline 索引堆，`Next()` 直接读取堆顶，不扫描全表。
- 以 `next` 为键维护 future 索引堆；唤醒时只把堆顶 `next <= w` 的定时器移入 ready 索引堆。
- ready 堆按 `(next+slack,id)` 排序，每次只弹出前 `B` 个；被触发的定时器更新后重新插入 future/deadline 堆，`Remove` 对三个堆做增量删除。
- 因此 `Next()` 的关键查询为常数次根访问；`Wake()` 的有序结构考察数为本次候选迁移数加实际弹出数及常数停止探测。

确定性测试包括：

- `n == w` 是候选而 `n == w+1` 不是。
- `w == next+slack` 时延迟为 0。
- `last+g == e` 取该共同时刻。
- `g=0` 时退化为按窗口末端唤醒。
- `B` 小于候选数时的留下、连续延迟与后续追赶。
- `k=floor((w-next)/period)` 的整数向下取整。
- 留下的候选在其窗口早于 `last+g` 时按下一栅格延迟唤醒。
- `Remove` 后 deadline 与候选立即重算。
- `Add` 的 `next == last` 合法。
- 2000 组随机操作序列与逐整数时刻检查、全量排序候选的朴素模型对拍；测试日志记录输入、输出和判定依据。

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
