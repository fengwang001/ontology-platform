# rollout — 带注入时刻的特性渐进发布控制器

`rollout.Controller` 按阶梯百分比与每级保持时长推进特性发布，支持暂停、恢复与
回滚；用户是否落入发布范围由盐值 + 用户标识的确定性 FNV-1a 分桶决定。所有时钟
时刻均由调用方注入（整数毫秒），因此阶段推进、暂停扣时与范围判定都可精确复现。

## 构造

```go
c, err := rollout.New("my-salt", []rollout.Step{
    {Percent: 10, HoldMs: 100},
    {Percent: 50, HoldMs: 200},
    {Percent: 100, HoldMs: 0}, // 最后一级 Hold 被忽略，但仍须 >= 0
})
```

- `salt` 必须为非空字符串；阶梯至少 2 级，百分比为 1..100 的整数且严格递增，
  每级 `HoldMs >= 0`（含最后一级，虽然其值不参与到期计算）。

## 状态机

状态为 `Idle / Running / Paused / Completed / RolledBack`。

- `Start(now)`：仅 `Idle` 或 `RolledBack` 可调用，进入第 0 级，
  `enteredAt = now`、本级累计暂停时长 `paused = 0`，状态 `Running`。
- `Pause(now)`：仅 `Running` 可调用，记录暂停起点；不推进阶段。
- `Resume(now)`：仅 `Paused` 可调用，将 `now - 暂停起点` 累加进当前级
  `paused`，回到 `Running`；不推进阶段。
- `Tick(now)`：仅 `Running` 下发生转移；可在一次调用内连续跨越多级。
  进入最后一级即 `Completed`。其他状态下 `Tick` 不报错也无转移。
- `Rollback(now)`：可在 `Running / Paused / Completed` 调用，状态变为
  `RolledBack`、级别归 -1、百分比归 0。之后可重新 `Start`，从第 0 级重新起算。
- `Status()` 返回状态、级别（`Idle / RolledBack` 为 -1）与当前百分比
  （`Idle / RolledBack` 为 0，`Paused` 沿用当前级百分比）。

## 到期时刻公式与暂停扣时

第 i 级（非最后一级）的到期时刻：

```
due_i = enteredAt_i + Hold_i + paused_i
```

其中 `paused_i` 是进入第 i 级之后、当前为止所有 Pause→Resume 区间
（`resumeAt - pauseAt`）之和。进入新一级时该级 `paused` 清零。

`Tick(now)` 时只要 `now >= due_i` 就转移；**转移时刻取 `due_i` 而不是
`now`**，且下一级 `enteredAt_{i+1} = due_i`、`paused_{i+1} = 0`。因此一次
Tick 跨越多级时，各转移时刻分别是各自的 due。例：

- `Start(0)`、`Pause(40)`、`Resume(90)`（第 0 级暂停扣时 50ms）、
  `Tick(1000)`：转移 `0→1 @ 150`（= 0+100+50）、`1→2 @ 350`（= 150+200），
  最终 `Completed`。
- Pause/Resume 不推进阶段：恰好在原始 due 暂停时，该级不会转移；恢复后 due
  按暂停时长顺延。

## 用户分桶

`InRollout(user)` 对字节序列 `salt + "\x00" + user` 计算 32 位 FNV-1a：

```
h = 2166136261
for b in bytes: h = (h XOR b) * 16777619 mod 2^32
bucket = h mod 10000
在范围内  <=>  bucket < 当前百分比 * 100
```

因为阶梯百分比严格递增且判定为固定前缀（`bucket < pct*100`），任一用户在阶段
升高时范围只增不减；`RolledBack/Idle` 时百分比为 0，全员不在范围内。`user`
为空整体拒绝（`ErrEmptyUser`）。

## 时钟回拨与错误优先级

每个带 `now` 的操作（`Start/Pause/Resume/Tick/Rollback`，含无转移的 Tick）都
先做时钟检查：`now` 小于此前任一**已接受**操作的最大 `now` 即返回
`ErrClockRewind`。检查顺序固定为：

1. 时钟回拨检查；
2. 状态检查（`ErrInvalidState`）。

即“回拨先于状态不符”。任一检查失败都整体拒绝，且不改变状态、暂停累计与最大
`now`（最大时刻只在校验通过、操作被接受的瞬间提交）。

构造参数校验顺序（只报第一个）：盐为空 → 阶梯少于 2 级 → 百分比越界 →
百分比不严格递增 → Hold 为负。

可区分的哨兵错误：`ErrEmptySalt`、`ErrTooFewSteps`、`ErrPercentOutOfRange`、
`ErrPercentNotStrictlyIncreasing`、`ErrNegativeHold`、`ErrClockRewind`、
`ErrInvalidState`、`ErrEmptyUser`（配合 `errors.Is` 使用）。

## 并发与确定性

所有方法通过控制器内部互斥量串行化，并发调用的结果等价于某个串行顺序；
`InRollout` 为纯函数式判定，相同输入必然得到相同结果。相同的操作序列重放得到
完全相同的转移列表与范围判定。

## 本地验证

```bash
# 全量测试
go test ./...

# 竞态检测 + 详细日志（对拍每组操作的输入、输出与判定依据）
go test -race -v ./rollout

# 只看 2000 组随机序列与朴素模拟器的对拍
go test -v -run TestDifferentialAgainstNaiveSimulator ./rollout

go vet ./...
gofmt -l .
```
