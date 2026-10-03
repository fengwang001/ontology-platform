# tlb：带 ASID 世代回绕与惰性整表刷新的多 CPU 软件 TLB 模型

`tlb` 包模拟多 CPU 平台上 ASID 容量有限时的地址空间切换与 TLB 行为。
相同操作序列重放得到完全相同的返回值、全局世代、各 mm 的 `(asid, gen)`
与各 CPU 的 TLB 内容。

## 世代与 ASID 的关系

- 全局世代 `G` 初值为 1，只增不减，仅在回绕时加一。
- 每个地址空间（mm）持有一对 `(asid, gen)`，初值 `(0, 0)`；`asid` 为 0 表示尚未分配。
- `mm.gen == G` 表示该 mm 的 ASID 在当前世代有效，`Switch` 直接沿用（快速路径）。
- 已占用集合 `taken` 恰好等于"当前世代内已分配且未销毁的 asid"与"被保留的 asid"之并。
- 同一世代内不同存活 mm 的 asid 互不相同。

## 回绕：保留集合与 pending

当 1..A 的 ASID 全部占用且仍需分配时发生回绕（一次 `Switch` 至多回绕一次）：

1. `G` 加一；
2. 每个 CPU 的 `reserved` 置为其当时的 `active`（包括正在切换的 CPU 切换前的值，可能为无）；
3. `taken` 重置为这些 `reserved` 对的 asid 集合（重复的只算一个）；
4. 所有 CPU 的 `pending` 置真；
5. 重新为该 mm 判定（提升或分配最小空闲 asid）。

`pending` 为真的 CPU 在其**下一次 `Switch`** 时才清空整个 TLB 并清除标记
（惰性整表刷新）；未发生回绕但 `pending` 仍为真的 `Switch` 同样会刷新。
`reserved` 只记录最近一次回绕时的活动对，更早的保留随之失效。

## 提升条件

当 `mm.gen != G` 且 `mm.asid != 0` 且 `(mm.asid, mm.gen)` **恰好等于**某个 CPU
的 `reserved` 对时，只把 `mm.gen` 提升为 `G` 而保留 asid，不改 `taken`。
asid 相同而 gen 不同的旧 mm 不被提升；不满足提升条件时分配 `taken` 之外
编号最小的 asid。

## TLB 淘汰与失效语义

- TLB 逐 CPU，容量 T，条目以 `(asid, vpn)` 为键，最近使用在前。
- `Fill` 以当前 `active` 的 asid 为标签插入或更新并置为最近使用；超过 T
  时淘汰最久未用者并返回其键。其他 ASID 的条目同样占用容量。
- `Lookup` 以当前 `active` 的 asid 查找，命中返回 pfn 并置为最近使用，
  未命中不改任何状态。
- `Invalidate(mm, vpn)` 仅当 mm 的上下文有效（`mm.gen == G`，或
  `(asid, gen)` 等于某个 `reserved` 对）时，从所有 CPU 删除键
  `(mm.asid, vpn)` 并返回删除了条目的 CPU 个数；否则返回 0 且不改任何状态。

## 销毁与 asid 释放

`DestroyMM` 在 mm 的 `(asid, gen)` 等于某个 CPU 的 `active` 时报 `ErrBusy`
且状态不变。否则标记销毁（编号不回收，仍占 M 的名额），并且仅当
`asid != 0`、`gen == G`、且 asid 不属于任何 `reserved` 对时立即释放：
从 `taken` 移除，并从所有 CPU 的 TLB 中删除全部该 asid 的条目。被释放的
asid 可在本世代内被新 mm 重新分到，旧条目不得被新 mm 命中。其余情形
（世代已旧，或 asid 仍属某个 `reserved` 对）只标记，不动 `taken` 与任何 TLB。

## 复杂度与计数器

- `Switch` 快速路径、`Fill`、`Lookup` 为 O(1)（ASID 分配用两级位图，
  TLB 用哈希表加双向链表）。
- 回绕为 O(C + A/64)，不遍历 mm 表。
- `Invalidate` 每个 CPU 至多探测 1 个条目（总数不超过 C）。
- `DestroyMM` 至多检查 C 个 `active`、C 个 `reserved`，并借助逐 CPU 的
  按 asid 索引只访问相关条目，不扫描无关条目、不遍历 mm 表。
- 非导出计数器 `mmVisited` 统计对 mm 表的遍历次数，任何操作都不得遍历
  mm 表，故恒为 0（`TestNoMMTableScan` 在 10^5 个 mm 上做 1000 次回绕验证）；
  `invalidateProbes` 统计 `Invalidate` 的探测次数，每次调用不超过 C。

## 并发

所有方法可并发调用，内部以单一互斥锁串行化，结果等价于某个串行顺序；
回绕的各步对外表现为一个原子步骤，观察者看不到只改了一部分
`reserved` 或 `pending` 的中间状态。

## 本地验证

```bash
go test ./tlb/                 # 全部测试（含 2000 组随机序列对照朴素模拟）
go test -race ./tlb/           # 竞态检测
go test -v -run TestRandomAgainstNaive ./tlb/   # 打印每步输入、输出与判定依据
go test -v -run TestSpecExample ./tlb/          # 题目示例逐步走查
go vet ./tlb/ && gofmt -l tlb/
```
