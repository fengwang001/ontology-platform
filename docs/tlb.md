# 多 CPU 软件 TLB 模型（`tlb` 包）

`tlb` 包实现了一个带 ASID 世代回绕与惰性整表刷新的多 CPU 软件 TLB
模型。所有操作可并发调用，效果等价于某个串行顺序；相同操作序列重放
得到完全相同的返回值与内部状态。

## 世代与 ASID 的关系

- 全局世代 `G` 初值为 1，只增不减。
- 每个地址空间（mm）携带 `(asid, gen)`，初始 `(0, 0)`；`asid` 为 0
  表示尚未分配。可用 ASID 为 `1..A`。
- `mm.gen == G` 时该 mm 的绑定在当前世代有效，`Switch` 直接沿用
  （快速路径）。
- `taken` 是当前世代已分配且未释放的 ASID 与被保留 ASID 的集合，
  用位图维护；新分配总是取 `taken` 之外编号最小的 ASID。
- 同一世代内，不同存活 mm 的 ASID 互不相同。

## 回绕：保留集合与 pending

当 `1..A` 无空闲 ASID 且无法提升时发生回绕（一次 `Switch` 至多一次），
回绕是一个原子步骤，任何观察者都看不到中间状态：

1. `G` 加一；
2. 每个 CPU 的 `reserved` 置为它当时的 `active`（包括正在切换的 CPU
   切换前的值，可能为无）；`reserved` 只记录最近一次回绕时的活动对；
3. `taken` 重置为这些 `reserved` 对的 ASID 集合（重复的对只占一位）；
4. 所有 CPU 的 `pending` 置真。

因为 `A > C`，回绕后 `taken` 至多有 `C` 个 ASID，必然有空闲，所以
一次 `Switch` 不会回绕两次。

`pending` 是逐 CPU 的惰性刷新标记：回绕时全部置真，但只有在该 CPU
下一次 `Switch` 时才真正清空其整个 TLB 并清除标记（本次返回
`flushed=true`）。未切换的 CPU 保留旧条目，仍可按其旧 `active` 的
ASID 命中。被刷新过的 CPU 在下一次 `Switch` 之前其 TLB 为空。

## 提升条件

`mm.gen != G` 且 `mm.asid != 0` 时，当且仅当 `(mm.asid, mm.gen)` 与
某个 CPU 的 `reserved` 对**完全相等**才发生提升：只把 `mm.gen` 改为
`G`，保留原 ASID，不改 `taken`。ASID 相同而世代不同的旧 mm 不被
提升，按普通分配处理。

## TLB 淘汰与失效语义

- TLB 逐 CPU，容量 `T`，键为 `(asid, vpn)`，最近使用在前。
- `Fill` 以该 CPU 当前 `active` 的 ASID 为标签插入或更新并置为最近
  使用；超过 `T` 时淘汰最久未用者并返回其键。所有 ASID 的条目共享
  同一容量。
- `Lookup` 命中返回 `pfn` 并置为最近使用；未命中不改任何状态。
- `Invalidate(mm, vpn)` 仅当 mm 的上下文有效时生效：`mm.asid != 0`
  且（`mm.gen == G` 或 `(asid, gen)` 恰好等于某个 `reserved` 对）。
  从所有 CPU 删除 `(mm.asid, vpn)`，返回删除条目的 CPU 数；否则
  返回 0 且不改任何状态。

## 销毁与 ASID 释放

`DestroyMM` 不回收编号（仍占 `M` 名额）：

- 若 `mm.asid != 0`、`mm.gen == G` 且某 CPU 的 `active` 恰等于
  `(mm.asid, mm.gen)`，报 `ErrBusy`，状态不变；
- 否则标记为已销毁。当 `mm.asid != 0`、`mm.gen == G` 且该 ASID 不
  属于任何 `reserved` 对时立即释放：从 `taken` 移除，并从所有 CPU
  的 TLB 删除全部该 ASID 的条目（借助逐 CPU 的按 ASID 索引），返回
  `(true, 删除条目数)`。释放的 ASID 可在本世代被新 mm 重用，旧条目
  不会被新 mm 命中；
- 其余情形（世代已旧，或 ASID 仍属某 `reserved` 对）只标记，不动
  `taken` 与任何 TLB，返回 `(false, 0)`。

## 错误顺序

- `Switch`：`ErrBadCPU`、`ErrBadMM`、`ErrDead`；
- `Fill` / `Lookup`：`ErrBadCPU`、`ErrInvalidParam`（vpn/pfn 超出
  `0..2^32-1`）、`ErrNoContext`；
- `Invalidate`：`ErrBadMM`、`ErrDead`、`ErrInvalidParam`；
- `DestroyMM`：`ErrBadMM`、`ErrDead`、`ErrBusy`。

被拒绝的操作不改变 `G`、`taken`、`reserved`、`pending`、`active`、
任何 mm 与任何 TLB（含最近使用次序）。

## 复杂度与计数器

- `Switch` 快速路径、`Fill`、`Lookup`：O(1)（位图至多 64 个机器字，
  视为常数）。
- 回绕：O(C + A/64)，不遍历 mm 表。
- `Invalidate`：每个 CPU 至多探测 1 个条目（哈希定位），总探测数
  不超过 C，不扫描 TLB。
- `DestroyMM`：至多检查 C 个 `active`、C 个 `reserved`，以及各 TLB
  中标签为该 ASID 的条目（逐 CPU 按 ASID 索引），不扫描无关条目，
  不遍历 mm 表。
- 非导出计数器 `mmVisited` 记录对 mm 表的遍历次数，必须恒为 0；
  测试 `TestMMVisitedZero` 在 10^5 个 mm 上执行 1000 次回绕后断言
  其仍为 0。

## 本地验证

```bash
go test ./tlb/                 # 全部单元测试 + 2000 组随机对照
go test -race -v ./tlb/        # 竞态检测 + 逐步日志（输入/输出/判定依据）
go vet ./... && gofmt -l .
```

随机对照测试（`TestRandomizedVsNaive`）把 2000 组随机操作序列同时
重放到本实现与一个按规则逐步写成的朴素模拟（逐 mm 遍历、逐条目线性
查找），逐操作比较返回值并比较最终 `G`、各 mm 的 `(asid, gen)`、
`taken`、`reserved`、`pending` 与各 TLB 内容。
