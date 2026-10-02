# consistenthash — 带负载上限的一致性哈希环

包路径：`ontology/consistenthash`

所有方法由内部互斥锁串行化，并发调用的结果等价于某一种全序串行执行；
重放完全相同的操作序列得到完全相同的放置、负载与迁移清单。

## 构造

`New(cnum, cden int64) (*Ring, error)`，负载系数 `c = cnum/cden`。
仅当 `1 ≤ cden ≤ cnum ≤ 1_000_000` 时合法，否则返回 `ErrInvalidConfig`，
对象不创建。

## 上限公式

- `Put`：放置前已放置键数为 `K`、现存节点数为 `N`，
  `cap = ⌈ cnum·(K+1) / (cden·N) ⌉`（用 **K+1**，即包含本次将放入的键）。
- `RemoveNode` 重放置：已重放成功的键数为 `Kc`（每放一个 `Kc` 加一），
  `cap = ⌈ cnum·(Kc+1) / (cden·(N-1)) ⌉`。**不能**固定使用总键数，
  否则中间键会错误地提前落在高负载节点（规范示例中的 k3 即如此区分）。
- `Rebalance`：`capR = ⌈ cnum·K / (cden·N) ⌉`（用当前总数 **K**，不加一）。

## 放置与回绕

1. 在已排序的点序列上对 `pos` 做二分（`lower_bound`），得到第一个
   位置 `≥ pos` 的点作为起点；若所有点都小于 `pos`（越过最大点），
   回绕到最小点。
2. 从起点顺时针逐点考察：点所属节点当前负载 `≥ cap` 即视为已满并跳过
   （负载**等于** cap 也算满）；落在第一个负载 `< cap` 的点所属节点。
3. 键领取单调递增的放置序号 `seq`（从 1 起）；只有成功的 `Put` 领取
   seq，迁移（`RemoveNode`/`Rebalance` 引起的重放置）保持原 seq 不变。

起点二分的每次键比较都由非导出计数器统计，每次定位的比较次数不超过
`⌈log2 P⌉ + 1`（P 为环上点数；顺时针顺延跳过满节点不计入）。

## 节点增删

- `AddNode(id, points)`：`id ≥ 1`，`points` 为 1～64 个全环唯一的
  `uint64`（同一节点内也不可重复）。只登记节点与点，**不移动任何键**。
- `RemoveNode(id)`：先摘除该节点全部点，再取出其名下全部键（总数与
  节点数先扣减），按 **seq 升序**逐个按放置规则重放置；每放一个
  `Kc` 加一，cap 用放置当时的 `Kc`。最后一个节点且其名下仍有键时
  拒绝（`ErrLastNodeBusy`）；无键时允许清空。

## 再平衡

`Rebalance(limit)`（`0 ≤ limit ≤ 10^9`，0 表示不迁移）：

1. 用固定 `capR`（见上）。
2. 按**节点编号升序**遍历；对负载 `> capR` 的节点，反复取它名下
   **seq 最大**的键：先从该节点摘除（负载减一，因此原节点仍
   `≥ capR`，重放置时必然被跳过），再从该键的 `pos` 起按放置规则
   落到其它节点。
3. 直到该节点负载等于 `capR`，或累计迁移次数达到 `limit`——达到即
   **整体停止**（后续节点不再处理）。
4. 返回迁移清单 `(键, 原节点, 新节点)`（按发生次序）与剩余超额
   `Σ max(0, 负载 − capR)`。

`Delete(key)` 只移除该键、所属节点负载减一，不改变其它键，也不触发
任何迁移。`Lookup(key)` 返回所属节点 id。

## 错误报告次序（只报第一个，拒绝即不改状态）

- `Put`：key 为空 → 无节点 → 键已存在。
- `AddNode`：参数非法（id<1、点数不在 1..64、自身重复）→ 节点已存在
  → 点冲突。
- `RemoveNode`：节点不存在 → 最后一个节点且仍有键。
- `Delete`/`Lookup`：key 为空 → 键不存在。
- `Rebalance`：limit 越界 → 无节点。

被拒绝的操作不改变任何节点、点、键、负载，也不消耗 seq。

## 不变量

- 任意时刻各节点负载之和等于现存键总数；每个键恰属于一个现存节点。
- 每次 `Put` 后被选节点负载不超过放置当时的 cap。
- `Rebalance` 返回剩余超额为 0 时每个节点负载不超过 capR。

## 本地验证

```bash
# 全量测试（含 -race）
go test -race ./...

# 详细日志：规范示例、二分计数、2000 组朴素模拟对照（打印输入/输出/判定）
go test -race -v -run 'TestWorkedExample|TestBisect|TestNaiveDifferential2000' ./consistenthash

# 覆盖率
go test -coverprofile=/tmp/cover.out ./...
go tool cover -func=/tmp/cover.out

gofmt -l .
go vet ./...
```

测试组成：

- `ring_example_test.go`：规范逐步示例（含 cap 上取整、回绕、等于 cap
  即满、AddNode 不移动、Delete 不迁移、limit 截断与剩余超额）。
- `bisect_internal_test.go`：在 `P = 1, 2, 3, 7, 100, 100000` 下验证
  每次二分比较次数 `≤ ⌈log2 P⌉+1`（100 档实测 7≤8，10^5 档 17≤18）。
- `naivemodel_test.go` + `differential_test.go`：独立按规范逐步写成的
  朴素参考实现，对照 2000 组随机的节点增删、Put/Delete、Rebalance
  序列，逐操作比对错误码、归属、负载、seq 与迁移清单，日志中打印每步
  输入、输出与最终判定依据。
