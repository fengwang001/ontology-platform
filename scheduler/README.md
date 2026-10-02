# 增量关键路径维护器

`scheduler.Scheduler` 在带搭接时距的任务依赖网络上维护 CPM 时间量、时差、关键任务集合、关键路径和 EF 基线。所有更新与查询均由读写锁串行化，查询只能看到完整更新后的状态。

## 构造与操作

- `New(N, E, D)`：任务上限 `1..100000`，依赖上限 `1..500000`，项目期限 `0..10^12`。
- `AddTask(dur)`：返回从 0 开始递增的任务编号；工期范围 `0..10^6`，初始 `snet=0`、无 `fnlt`。
- `AddDep(u, v, lag)`：定义 `ES(v) >= EF(u)+lag`；`lag` 范围 `-10^6..10^6`，负值表示提前搭接。
- `SetDuration(v, dur)`、`SetConstraint(v, snet, fnlt)`、`RemoveDep(u, v)` 执行增量更新。
- `fnlt=-1` 表示没有最晚完成约束；`snet` 范围为 `0..10^9`，`fnlt` 范围为 `-1..10^12`。

拒绝顺序：

- `AddDep`：参数非法、任务不存在、依赖已存在、依赖数已满、成环（含自环）。
- `RemoveDep`：任务不存在、依赖不存在。
- `AddTask`：参数非法、任务数已满。
- `Variance`：任务不存在、无基线。

被拒绝的操作不修改任务、依赖、计数器或基线。错误值分别为 `ErrInvalidArgument`、`ErrTaskNotFound`、`ErrDepExists`、`ErrTaskLimit`、`ErrDepLimit`、`ErrDepNotFound`、`ErrCycle`、`ErrNoBaseline`，可用 `errors.Is` 区分。

## 时间与时差定义

- `ES(v)=max(snet(v), min over predecessors: EF(u)+lag(u,v))`
- `EF(v)=ES(v)+duration(v)`
- `PF=max(EF(v))`；没有任务时为 0。
- `LF(v)=min(D, fnlt(v) if set, min over successors: LS(w)-lag(v,w))`
- `LS(v)=LF(v)-duration(v)`
- `TF(v)=LF(v)-EF(v)`，可为负。
- `FF(v)=min(ES(w)-lag(v,w)-EF(v))`；没有后继时为 `PF-EF(v)`。

关键任务是当前全部任务中最小 `TF` 的任务。因此期限紧张时所有 `TF` 可以为负，期限宽松时关键任务的 `TF` 也可以为正。

## 驱动边与关键路径

依赖 `(u,v)` 是驱动边，当且仅当：

```text
ES(v) == EF(u)+lag(u,v)
```

`CriticalPath()` 只经过关键任务和关键任务之间的驱动边：

- 起点取关键任务中没有“来自关键任务的驱动入边”的最小编号。
- 每一步从当前任务指向关键任务的驱动边终点中选择最小编号。
- 没有任务时返回空切片。

被 `snet` 抬高的 ES 可能使原来的依赖不再驱动；此时关键路径不会经过该边。

## 更新报告

每个被接受的更新返回 `UpdateReport`：

- `ChangedES`、`ChangedLF`：仅列已有任务中 ES 或 LF 实际变化的编号，升序。
- `OldPF`、`NewPF`：更新前后 PF。
- `CritAdded`、`CritRemoved`：进入、退出关键集合的编号，升序。

新增任务本身不会出现在 `ChangedES` 或 `ChangedLF` 中，但可能进入关键集合并使旧关键任务退出。最小 TF 的变化可能只改变关键集合，而不改变任何 ES/LF。

增量重算只从操作直接涉及的种子任务开始：

- 前向沿出边传播 ES/EF，后继候选值不再变化即停止。
- 后向沿入边传播 LF/LS，前驱候选值不再变化即停止。
- `fwdEval`、`bwdEval` 分别记录本次更新实际重算 ES、LF 的次数；被拒绝的操作保持计数器不变。

## 基线

`SetBaseline()` 保存当时所有任务的 EF，并覆盖此前基线。`Variance(v)` 返回当前 `EF(v)-baselineEF(v)`。没有基线、或基线之后新建的任务返回 `ErrNoBaseline`。

## 本地验证

```bash
GOCACHE=/tmp/go-cache-ontology go test ./...
GOCACHE=/tmp/go-cache-ontology go test -v ./scheduler
GOCACHE=/tmp/go-cache-race go test -race ./scheduler
```

测试包含：

- 题目给定示例、负搭接、snet/fnlt、紧/松期限、并列路径、自由时差、删除回落和拒绝不改状态。
- `n=1000` 与 `n=100000` 长链末端抬高 snet，`FwdEval=1`、`BwdEval=0`。
- 改工期后 LF 向前驱传播，遇到 fnlt 截断即停止。
- 2000 组随机更新序列，与每次更新后整网重算的朴素拓扑实现逐项对照。
- 并发更新/查询的 race 测试。

详细测试在失败或使用 `-v` 时打印输入、输出和判定依据。
