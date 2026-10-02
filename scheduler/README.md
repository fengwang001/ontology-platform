# 偏移区间调度器

`Scheduler` 登记半开偏移区间 `[from,to)`，按连续步进领取不可重复的偏移批，并支持把尚未领取的尾部拆成新的独立区间。

## 区间与批

- `Add(from,to)` 与 `Split` 共用递增的区间编号，编号从 1 开始且不复用。
- 每个区间保存不可变起点 `from`、可变上界 `to`、下一个待领取偏移 `next`，初始 `next=from`。
- `next==to` 表示可领取部分已经领完；区间还不能算完成，直到所有批都已确认。
- `Process(id,n)` 领取 `k=min(n,to-next)`，生成半开批 `[next,next+k)`，批号全局从 1 递增。
- `Ack(batchID)` 将批置为已确认；已确认批和已完成区间不再改变。

## 拆分点

令剩余量为：

```text
rem = to - next
keep = max(1, ceil(rem*num/den))
sp = next + keep
```

当 `sp>=to` 时返回 `ErrCannotSplit`，且不改变任何状态与编号计数。否则原区间变为 `[from,sp)`，新区间为 `[sp,to)`。

`ceil(x/y)` 向上取整：

```text
ceil(x/y) = q + (r != 0 ? 1 : 0), 其中 x=q*y+r, 0<=r<y
```

因此 `7/3=2.333...` 得 3 而不是四舍五入或向下取整；`num=0` 时数学值为 0，但 `keep` 仍由 `max(1,...)` 提升为 1。实现使用 `math/big` 计算乘积，支持 `rem*num` 超过 64 位整数的情况。

## 保持值与水位线

区间保持值为：

```text
hold(interval) = min(next, 所有未确认批的最小起点)
```

批未确认时，它的起点会持续压住保持值；乱序确认后面的批不会推进保持值，只有确认当前最早未确认批后，保持值才跳到下一个未确认批起点或 `next`。领完但仍有未确认批的区间未完成，仍参与全局水位。

输出水位线 `W` 初始为 0。每个真正改变状态且被接受的 `Add`、`Process`、`Split`、`Ack` 后：

- 若存在未完成区间，取所有未完成区间 `hold` 的最小值 `m`，令 `W=max(W,m)`。
- 若没有未完成区间，`W` 保持原值，不会跳到无穷大。
- 已完成区间不参与最小值。
- 水位线单调不减；新 `Add` 的 `from` 必须满足 `from>=W`。

调度器在每个区间内维护按批起点排序的最小堆，并维护未完成区间的全局 hold 堆。失效堆项在访问堆顶时惰性丢弃；未导出字段 `holdProbes` 统计这些堆访问，单次操作的额外有效探测不依赖未完成区间数或未确认批数。

## 拒绝原因

所有校验只返回第一个可区分的错误：

- `Add`：`ErrInvalidArgument`，然后 `ErrBelowWatermark`。
- `Process`/`Split`：`ErrInvalidArgument`、`ErrIntervalNotFound`、`ErrExhausted`；Split 还可能返回非拒绝性的 `ErrCannotSplit`。
- `Ack`：`ErrInvalidArgument`、`ErrBatchNotFound`、`ErrAlreadyAcknowledged`。
- `Progress`：`ErrIntervalNotFound`。

被拒绝的操作以及 `ErrCannotSplit` 都不修改区间、批、编号计数或水位线。

## 本地验证

```bash
go test ./...
go test -race -v ./scheduler
go test -v ./scheduler -run TestRandomSequencesAgainstNaiveModel
go vet ./...
```

随机测试重放 2000 组操作序列，与每次重扫全部区间和全部批求最小 hold 的朴素模型逐项对照；`-v` 日志包含每步输入、实际输出、期望输出和判定依据。
