# paxos：Multi-Paxos 新主槽位恢复规划器

新主上任时，以选票 `b` 向 `n` 个接受者（编号 `0..n-1`）收集承诺报告，
由 `Planner` 汇总出每个未决槽位必须沿用的值，为空洞补 Noop，并给出
下一个空闲槽位，使恢复计划可精确复现。

## 承诺报告（PromiseReport）

- `PB`：承诺选票，必须等于规划器选票 `b`。
- `Chosen`：已确定前缀，该接受者已知槽位 `1..Chosen` 的值都已确定；非负整数。
- `Accepted`：已接受表，`槽位 -> (接受选票, 值)`；值为字符串，可为空串
  （空串是真实接受值，与 Noop 不同）。

## start 与 nextFree 的推导

收到多数派（`⌊n/2⌋+1` 个不同接受者）报告后：

- `start = max(各报告 Chosen) + 1`：已确定前缀无需恢复。
- `maxSlot = 各报告 Accepted 中出现的最大槽位`（全部为空表时取 `0`）。
- 仅 `[start, maxSlot]` 内的槽位需要恢复；`maxSlot < start` 时无需恢复任何槽位。
  已接受表中槽位小于 `start` 的项被忽略。
- `nextFree = max(maxSlot, start-1) + 1`。

## 值的选择与空洞补 Noop

对 `[start, maxSlot]` 内每个槽位 `s`：

- 取各报告中该槽位**接受选票最大**的那一项，值与来源选票均来自该项
  （按最高选票而非多数派计数：单个选票 5 的接受项胜过两个选票 3 的相同值）。
- 若所有报告在该槽位都没有接受项，则为 Noop：来源选票记 `0`，与值为
  空串的真实接受项（选票 > 0）严格区分。

`Plan` 返回按槽位升序的条目（槽位、是否 Noop、值、来源选票）以及
`Start` 与 `NextFree`；成功后规划器关闭。返回值不与内部状态别名，
报告在到达时即被拷贝。

## 错误优先级

`AddPromise` 按以下顺序只报第一个错误，被拒绝的报告不改变任何状态：

1. `ErrClosed`：规划器已关闭；
2. `ErrFromOutOfRange`：`from` 不在 `[0, n)`；
3. `ErrBallotMismatch`：`PB != b`；
4. 已接受表按槽位升序逐项检查，每项先查槽位后查选票：
   - `ErrSlotNotAfterChosen`：槽位不大于该报告自身的 `Chosen`（含槽位 0）；
   - `ErrInvalidAcceptBallot`：接受选票为 `0` 或不小于 `b`；
5. `ErrDuplicatePromise`：同一接受者已提交过。

`Plan` 按以下顺序只报第一个错误，失败不关闭规划器：

1. `ErrClosed`：规划器已关闭；
2. `ErrNoMajority`：不足多数派；
3. `*ConflictError`：同一槽位上拥有最大接受选票的各项出现不同的值，
   报告槽位最小的冲突槽位；低于最大选票的项之间的差异不算冲突。

构造时 `b` 或 `n` 非正会被 `NewPlanner` 拒绝。所有方法可并发调用，
效果等价于某个串行顺序；报告到达顺序不影响结果；并发 `Plan` 至多一个
成功，其余收到 `ErrClosed`。

## 本地验证

```bash
# 全部测试（含竞态检测）
go test -race ./paxos

# 查看对拍日志（输入、输出与逐槽位判定依据）
go test -v -run TestPlanAgainstNaive ./paxos

# 指定用例
go test -run TestHighestBallotWinsOverMajorityCount ./paxos
```
