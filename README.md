# 复制日志快照与截断协调器

领导者侧的本地模拟实现，只维护日志、提交/应用进度、快照边界和跟随者计划，不包含网络、RPC 或持久化。核心代码位于 `coordinator.go`，测试位于 `coordinator_test.go` 与 `model_test.go`。

## 构造参数

`New(T, Thr, L)` 创建协调器：

- `T`：截断后仍保留在日志中的尾部条目数，必须满足 `T >= 0`。
- `Thr`：自动快照阈值，必须满足 `Thr >= 1`；`applied - snapIndex >= Thr` 时立即快照，恰等也触发。
- `L`：近邻保护允许的落后条数，必须满足 `L >= 0`；`last - match <= L` 时跟随者仍受保护。

非法构造参数返回 `ErrParam`。

## 状态量

日志条目索引从 1 开始递增，每条记录其任期。任何成功操作后均保持：

```text
baseIndex <= snapIndex <= applied <= commit <= last
```

- `last`：最后一条日志索引。
- `commit`：已知已提交的最高索引。
- `applied`：本地状态机已应用的最高索引。
- `snapIndex`、`snapTerm`：最新快照包含的最后索引及其任期。
- `baseIndex`、`baseTerm`：已删除前缀的最后索引及其任期；仍存储的第一条索引是 `first = baseIndex + 1`。
- `removed`：非导出计数器，记录实际删除条目总数，增量始终等于 `baseIndex` 的增量。

`TermAt(i)` 对 `i == baseIndex` 返回 `baseTerm`；`i < baseIndex` 返回 `ErrCompacted`；`i > last` 返回 `ErrRange`。

## 快照与截断

自动快照由 `Apply` 在同一原子临界区内完成：更新 `applied`、判断阈值、设置 `snapIndex/snapTerm`、执行 `Compact`。手动 `Snapshot` 要求 `applied > snapIndex`，否则返回 `ErrNoProgress`。

`Compact` 的候选截断点为：

```text
cut = max(0, snapIndex - T)
```

随后依次施加固定点：

- 每个有在途快照的跟随者：`cut = min(cut, s)`，其中 `s` 是该在途快照索引。
- 每个无在途快照且满足 `last - match <= L` 的跟随者：`cut = min(cut, match)`。
- 落后超过 `L`（即 `last - match > L`）的跟随者不做近邻保护。

仅当 `cut > baseIndex` 时才截断。删除前先读取 `TermAt(cut)` 保存为新的 `baseTerm`，然后把 `baseIndex` 推进到 `cut` 并删除所有不大于 `cut` 的条目。`baseIndex` 单调不减。

`Compact` 会在每次成功快照、`FinishSnapshot`、`AbortSnapshot`、`DropPeer` 以及显式调用 `Compact` 时评估。`Ack` 与 `Retreat` 不触发截断；近邻条件只在实际执行 `Compact` 时读取。

## 跟随者计划

`AddPeer(id)` 登记 `match=0`、`next=last+1`。

- `Ack(id, m)`：要求当前 `match <= m <= last`；成功后 `match=m`、`next=m+1`。相等的确认可重复成功。
- `Retreat(id, n)`：要求当前 `match < n <= next`；成功后只更新 `next=n`。
- `Plan(id)` 的判定顺序为：
  1. 有在途快照：返回 `Installing`，携带在途快照的 `s` 和任期。
  2. `next <= baseIndex`：返回 `NeedSnapshot`，携带当前 `snapIndex/snapTerm`。
  3. 否则返回追加计划：`PrevIndex=next-1`、`PrevTerm=TermAt(next-1)`、`From=next`、`To=last`。

取等规则：`next == baseIndex` 需要快照；`next == baseIndex+1 == first` 仍可追加，此时 `PrevTerm` 是 `baseTerm`，不是 `snapTerm`。`To < From` 表示空的追加探测。

## 在途快照

- `StartSnapshot(id)`：仅当计划为 `NeedSnapshot` 时成功，记录固定点 `s=snapIndex` 并返回 `(s, snapTerm)`。每个跟随者至多一个在途快照。
- `FinishSnapshot(id)`：先解除固定，再令 `match=max(match,s)`、`next=match+1`，随后执行 `Compact`。
- `AbortSnapshot(id)`：先解除固定并执行 `Compact`，但不改变 `match` 与 `next`。
- `DropPeer(id)`：移除跟随者及其固定点，然后执行 `Compact`。

被拒绝的操作不改变任何状态，也不会触发 `Compact`。

## 错误与判定顺序

- 参数非法（包括空 `id`）优先返回 `ErrParam`。
- 未知跟随者返回 `ErrUnknownPeer`。
- 重复登记返回 `ErrExists`。
- 任期回退返回 `ErrTerm`；任期小于 1 返回 `ErrParam`。
- 范围错误返回 `ErrRange`。
- 无新应用进度时手动快照返回 `ErrNoProgress`。
- `StartSnapshot` 的顺序是 `ErrUnknownPeer`、`ErrInFlight`、`ErrNotNeeded`。
- `FinishSnapshot` 与 `AbortSnapshot` 没有在途快照时返回 `ErrNotInFlight`。

所有公开方法都由同一把互斥锁保护，结果等价于某个合法串行顺序；`Apply` 的应用、自动快照和截断是一个不可分割的原子步骤。

## 本地验证

如果 `go` 不在默认 `PATH`，可使用本机工具链：

```bash
export PATH=/usr/local/go/bin:$PATH
export GOCACHE=/tmp/go-cache

go test ./...
go test -race -v ./...
go test -run TestRandomOperationsMatchNaiveModel -v ./...
gofmt -l .
go vet ./...
```

随机测试固定种子 1 到 2000，重放 2000 组随机操作序列。它将实际实现与按上述规则逐步计算的朴素模型对照，比较日志边界、快照固定点、每个跟随者的 `match/next/在途快照` 和计划；`-v` 会输出每组输入、输出与截断/计划判定依据。
