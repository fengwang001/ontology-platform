# 多收件人消息投递回执状态机

`Tracker` 使用软退信阈值 `S` 构造，`S` 的范围是 1 到 16。所有公开方法都由同一个互斥锁保护，可并发调用，效果等价于某个串行顺序。

## 登记与查询

- `Send(msg, rcpts, deadline)`：`msg` 必须是非空且未登记的字节串；`rcpts` 为 1 到 32 个互不相同的非空收件人；`deadline` 在 0 到 10^12。
- `Receipt(msg, rcpt, kind, attempt, ts)`：`kind` 为 `sent`、`delivered`、`read`、`soft`、`hard`；`attempt` 为 1 到 16；`ts` 为 0 到 10^12。
- `Tick(now)`：`now` 在 0 到 10^12，且不得小于上一次成功 Tick 的时间。
- `Status(msg)`：返回整条消息汇总状态，并按登记顺序返回各收件人的 `(r, fail, late)`。

每个收件人维护：

- `r`：0 无、1 sent、2 delivered、3 read，只按进展值取最大值。
- `fail`：无、`soft`、`hard`、`exp`。
- `sentA`：已见 `sent` 回执中的最大尝试号，初值为 0。
- `softSet`：已见且通过当时分支处置的软退信尝试号集合。
- `seen`：已见的 `(kind, attempt)` 集合。
- `late`：已达到 `r >= 2` 后收到硬退信时置真。

## 回执处置次序

每条回执先查 `seen`：

1. `(kind, attempt)` 已存在：返回 `Duplicate`，不改变任何状态。
2. 否则立即写入 `seen`，再按 kind 与当前状态处置。

因此，被接受的新 `(kind, attempt)` 即使结果为 `Ignored` 或 `Stale` 也不会再次产生非 `Duplicate` 结果；四种结果计数之和等于已成功接受的回执总数。

`sent`、`delivered`、`read`：

- `fail` 为 `soft` 或 `hard`：`Ignored`，进展不再变化。
- `fail` 为 `exp`：只有 `delivered` 或 `read` 且 `ts < deadline` 才撤销到期；撤销后清除 `exp` 并取 `r=max(r, 进展值)`，返回 `Applied`。`ts == deadline` 或更晚，以及 `sent`，均为 `Ignored`。
- 无失败：`sent` 总是先执行 `sentA=max(sentA, attempt)`，再尝试推进 `r`；`r` 增大为 `Applied`，否则为 `Stale`。

`soft`：

- 当前已有任意失败标记：`Ignored`。
- `r >= 2`：`Stale`，不加入软退信计数。
- 否则把尝试号加入 `softSet`。计数为 `softSet` 中满足 `attempt >= sentA` 的元素数；计数达到 `S` 时置 `fail=soft`。无论是否达到阈值，该新回执均返回 `Applied`。

`hard`：

- `fail` 为 `soft` 或 `hard`：`Ignored`。
- `r >= 2`：只置 `late=true`，返回 `Stale`，不改变 `r` 或失败标记。
- 否则置 `fail=hard`，包括从 `exp` 升级为 `hard`，返回 `Applied`。

## 软退信作废规则

`sentA` 表示最近一次已知发送尝试。重新发送后，尝试号严格小于 `sentA` 的旧软退信不再参与阈值计数，但仍保留在 `softSet` 中以保证回执幂等。

例如 `S=2`：

- 先收到 `soft#1`，`cnt=1`。
- 后收到 `sent#2`，`sentA=2`，旧 `soft#1` 作废。
- 再收到 `soft#2`，集合为 `{1,2}`，但只有 `2 >= sentA`，`cnt=1`。
- 再收到 `soft#3`，`cnt=2`，置 `fail=soft`。

如果 `sent#1` 先到，随后 `soft#1`、`soft#2`，则两次软退信都有效，第二次达到阈值。

## 到期与可撤销性

`Tick(now)` 只在本次 `now` 与上次成功 Tick 不同且不回退时扫描。所有满足 `deadline <= now` 的消息中，`fail` 为无且 `r < 2` 的收件人置为 `exp`。返回的新到期名单按消息字节序、再按收件人字节序排序。重复 Tick 不会重复返回或重复修改状态。

`exp` 不是终局：

- `delivered` 或 `read` 的回执时间戳严格小于截止时刻时可撤销 `exp`。
- `sent` 不能撤销；软退信不能撤销。
- 硬退信会把 `exp` 升级为不可撤销的 `hard`。

## 汇总状态

对一条消息，设：

- `N`：收件人数。
- `F`：`fail != 无` 的人数。
- `pending`：`fail == 无` 且 `r < 2` 的人数。

按以下顺序判定：

1. `F == N`：`failed`。
2. `pending > 0`：`inflight`。
3. 其余情况下 `F > 0`：`partial`。
4. `F == 0` 且全体 `r == 3`：`read`。
5. 否则：`delivered`。

已送达后的硬退信只让对应收件人的 `late=true`，不会增加 `F`；因此它不改变消息汇总状态。

## 拒绝顺序

拒绝操作不会改变任何状态，包括 `seen`、收件人状态和 Tick 时钟。

- `Send`：先报参数非法，再报消息已存在。
- `Receipt`：先报参数非法，再报消息不存在，最后报收件人不属于该消息。
- `Tick`：先报参数非法，再报时钟回退。
- `Status`：先报参数非法（空消息），再报消息不存在。

## 本地验证

```bash
go test ./...
go test -race -v ./...
go vet ./...
gofmt -l .
```

`TestRandomSequencesAgainstNaiveOracle` 使用固定种子重放 2000 组随机登记、回执、Tick、Status 序列，并与测试内按上述规则逐条编写的朴素模型比较完整状态（`r`、`fail`、`sentA`、`late`、`softSet`、`seen`）、处置结果、到期名单和汇总状态。使用 `-v` 时会打印每条输入、输出与判定依据。
