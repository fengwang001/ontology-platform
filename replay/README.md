# replay — 整数令牌桶限速回放器

`replay` 包用**整数令牌桶 + 逻辑时钟**对队列事件做确定性、可复现的限速回放：
事件先进先出（FIFO）入队，按桶中令牌逐个吐出，从而把突发流量平滑成稳定节奏，
并在追赶积压时对吐出速率封顶。

## 模型

配置（`replay.Config`，速率单位为“令牌 / 逻辑时间单位”）：

- `BaseRate`：基准补充速率，**队列空时**使用，必须为正。
- `CatchupRate`：追赶补充速率，**队列有积压时**使用，必须为正且不小于 `BaseRate`。
- `BucketCap`：桶容量，令牌补后封顶值，必须为正。它同时决定单拍最多突发吐出的事件数。
- `QueueLimit`：队列上限，队列中事件数不得超过它，必须为正。

规则：

- 逻辑时钟只随 `Advance` / `Enqueue` / `Drain` 单调推进；每次推进按经过时间补充整数
  令牌，补充速率在补充前按队列状态选择（有积压取 `CatchupRate`，空队列取 `BaseRate`），
  补后令牌封顶到 `BucketCap`。
- 事件严格按入队顺序排队；吐出时从队首开始，**每事件扣一个令牌**，直到令牌耗尽或队列清空。
- 令牌在队列排空时不会丢弃，而是留在桶中（仍受桶容量封顶），因此空闲期攒下的令牌可用于
  削平下一波突发的头部，但突发最多只能吐出 `BucketCap` 个事件。

## 失败原因（可区分，失败即无副作用）

所有失败都以哨兵错误返回，可用 `errors.Is` 区分：

- `ErrClockBackwards`：传入时间戳小于当前时钟（时钟回退）。
- `ErrInvalidConfig`：基准速率、追赶速率、桶容量、队列上限非法（非正，或追赶速率小于基准速率）。
- `ErrQueueFull`：整批入队会使队列长度超过 `QueueLimit`。

任何一次失败都**先校验、后改状态**，因此时钟、令牌、队列、累计计数均保持失败前的值；
超限入队是整批拒绝，不会出现“入了一半”。

## 并发与不变量

所有状态变更串行化；查询接口（`Snapshot`、`Len`、`Tokens`、`Clock`、`Totals`）
与自检 `SelfCheck` 使用读锁，可被并发调用。`Snapshot` 在同一读锁临界区内取齐
时钟、令牌、队列长度与累计计数，因此并发读到的各字段必然互相对得上。

`SelfCheck` 校验：

- `0 <= Tokens <= BucketCap`；
- `0 <= Pending <= QueueLimit`；
- `累计入队数 EnqueuedTotal == 累计吐出数 DrainedTotal + 当前队列长度 Pending`。

## 本地验证：逐步重放三元组

单测 `TestStepwiseReplayTriples` 用一组**重放三元组**驱动同一个回放器，逐步核对结果：

```
(操作@时钟, 入队事件, 期望吐出序列)
```

配置为基准 1/tick、追赶 2/tick、桶容量 5、队列上限 10，脚本为：

| 三元组 | 期望吐出 | 判定依据 |
| --- | --- | --- |
| `Enqueue@0`, `[1 2 3]` | — | t=0 入队 3 个，初始令牌 0 |
| `Drain@0` | `[]` | 经过时间 0，无令牌 |
| `Drain@1` | `[1 2]` | 有积压，按追赶速率补 2 个令牌 |
| `Enqueue@1`, `[4..10]` | — | 同一时刻入队，不补令牌 |
| `Drain@2` | `[3 4]` | 再补 2 个令牌，FIFO 继续 |
| `Drain@5` | `[5 6 7 8 9]` | 积压 3 tick 应补 6 个，桶容量封顶为 5 |
| `Drain@6` | `[10]` | 补 2 个，仅余 1 个事件，另 1 令牌留存 |
| `Advance@10` | — | 队列空，按基准速率补 4 个，1+4 封顶为 5 |

每条用例日志都会打印**操作、时钟（前后）、令牌（前后）、吐出序列与判定依据**；
最终再核对 `入队总数 == 吐出总数 + 当前队列长度`。

其他用例分别覆盖：追赶按封顶速率补充（`TestCatchupRefillCapped`）、
突发按桶容量拆平（`TestBurstSmoothing`）、队列超限整批拒收（`TestQueueFullRejectsWholeBatch`）、
时钟回退（`TestClockBackwardsRejected`）、非法参数（`TestInvalidConfig`）、
严格保序（`TestFIFOOrder`）与并发读自检（`TestConcurrentReadsAndSelfCheck`）。

```bash
# 详细日志查看逐步重放过程
go test -race -v ./replay

# 只看三元组重放
go test -race -v -run TestStepwiseReplayTriples ./replay
```

## 快速上手

```go
r, err := replay.New(replay.Config{
    BaseRate:    1,  // 空闲时 1 令牌/tick
    CatchupRate: 5,  // 有积压时 5 令牌/tick
    BucketCap:   10, // 最多突发 10 个事件
    QueueLimit:  1000,
})
if err != nil { /* 参数非法 */ }

_ = r.Enqueue(0, []int64{1, 2, 3}) // 逻辑时刻 0 入队
events, err := r.Drain(2)          // 推进到时刻 2 并按令牌吐出
```
