# 版本向量增量反熵同步（vvsync）

`vvsync` 包实现多副本键值存储的增量反熵（anti-entropy）同步：任意一组注册副本
按任意顺序完成两两双向同步后，版本向量、变更日志与读视图完全一致；同步只发送
目标副本尚未见过的最小差集，结果可复现，并支持多执行体并发调用。

代码：`vvsync/vvsync.go`，测试：`vvsync/vvsync_test.go`。

## 数据模型

- `Change`：一条本地写入，字段为 `Origin`（来源副本 id）、`Seq`（该来源下
  从 1 开始严格递增的序号）、`Key`、`Value`、`Timestamp`。
  `(Origin, Seq)` 全局唯一标识一条变更。
- `VersionVector`：`map[来源副本id]最高已见序号`。从未见过的来源在向量中
  **缺省**（隐式为 0）；向量中永远不写入 0 值条目。
- `Replica`：持有 `seq`（自己来源的下一序号）、`vector`、按应用顺序保存的
  变更日志 `log`。

## 本地写入规则

`Replica.Write(key, value, ts)`：

1. 检查 `len(log)+1 <= maxLog`，超限直接返回 `log_limit_exceeded`，
   不递增序号、不追加日志（失败不留痕）。
2. 自己来源序号 `seq++`，生成 `(本副本id, seq)` 变更并追加日志。
3. 把向量中本来源条目推进到该序号。

## 同步协议（`Cluster.SyncOnce(source, target)`）

1. 目标把自己当前版本向量的副本发给来源。
2. 来源计算**最小差集**：对来源日志中每条变更，当且仅当
   `ch.Seq > targetVector[ch.Origin]`（目标向量缺省该来源时按 0）时入选；
   其余一律不发。因此重复同步发送 0 条。
3. 差集按 `(Origin 字典序, Seq 升序)` 排序后发出，判定依据为“每来源序号严格
   大于目标已见值”。
4. 目标对整批做校验，全部合法后一次性提交；任一非法条件成立则整批拒绝，
   日志与向量保持同步前状态。

目标侧校验顺序：

- 每条变更的 `Origin` 必须是本集群已注册副本；
- 批次必须严格按 `(Origin, Seq)` 有序；对每个来源，首条序号必须等于目标
  当前已见值 + 1，后续逐条 +1——即序号必须连续，无空洞、无重复、无回退；
- 应用后日志条数不得超过 `maxLog`（对整批原子判定）。

提交时仅在真正收到某来源的变更后才写入/推进该来源的向量条目：从未产生变更
的来源（包括已知但沉默的副本）不会出现在任何向量中。

`SyncBoth(a, b)` 是两个方向各执行一次 `SyncOnce` 的双向交换。

## 读视图取值（`Replica.View()`）

对每个键，在所有已见变更中取 `(Timestamp, Origin, Seq)` **字典序最大**
（先比时间戳，相等再比来源名，再比序号）的那条变更的值。
`(Origin, Seq)` 唯一标识变更，因此该规则构成全序，冲突解决结果确定、可复现。

## 收敛性

变更一旦生成不可变；来源序号稠密连续，同步保证目标最终按序补齐每个来源的
全部序号。只要两两同步的边集把副本图连通（重复、乱序均可），每个副本最终
都会见到所有 `(Origin, Seq)`，于是：

- 向量逐来源等于该来源写入总数；
- 规范化日志（按 `(Origin, Seq)` 排序）逐元素相同；
- 读视图因全序比较规则而相同。

## 边界与错误类别

所有拒绝都返回 `*SyncError`，其 `Reason` 取值互不相同、可编程区分；
任何一次拒绝都不改变任何副本的日志或向量：

| Reason | 触发条件 |
| --- | --- |
| `unknown_replica` | 同步任一端未在本集群注册（含来自其他集群的同名副本、nil），或批次中含未注册来源的变更 |
| `non_contiguous_changes` | 序号非正、批次未按 `(Origin, Seq)` 严格有序，或某来源出现空洞/重复/回退（未从已见值+1连续开始） |
| `invalid_version_vector` | `Cluster.ValidateVector` 发现向量条目为负、为 0（缺省来源必须省略）、命名了未注册副本，或声称的序号超过该来源日志实际到达值 |
| `log_limit_exceeded` | 本地写入或应用整批变更后日志条数会超过集群 `maxLog` |

其他边界：空向量合法且不引入任何来源；空差集同步为纯 no-op；同步是幂等的。

## 并发模型

- 每个 `Replica` 一把互斥锁保护向量与日志；同步时按集群注册序 `rank` 全局
  统一的先后顺序获取双方锁，避免死锁。
- 集群注册表由 `Cluster.mu` 保护；决策日志由独立的 `Cluster.logMu` 串行化，
  不与副本锁构成环。
- 多 goroutine 并发写入与并发双向同步可安全同时进行。

## 决策日志

默认写 stderr，可用 `Cluster.SetLogWriter(io.Writer)` 重定向。每次同步打印：

- `[sync-begin]`：来源、目标、目标版本向量；
- `[sync-diff]`：发送条数、具体变更列表、入选判定依据；
- `[sync-accept]` / `[sync-reject]`：应用条数与目标新向量，或拒绝原因与
  `state=unchanged`。

本地写入与注册也各有一条日志。

## 本地验证

```bash
# 若 go 不在 PATH
export PATH=$PATH:/usr/local/go/bin
# 只读环境下若默认构建缓存不可写
export GOCACHE=/tmp/gocache

go test -v ./vvsync/
go test -race -count=3 ./...
go vet ./...
gofmt -l .
```

测试覆盖：

- 差集最小且二次同步为 0（`TestMinimalDiff`）；
- 未知/沉默来源不产生向量条目（`TestUnknownSourceNoVector`）；
- 读视图时间戳/来源字典序决胜（`TestReadViewWinner`）；
- 四类非法输入各自的拒绝原因，以及拒绝后日志、向量、序号完全不变
  （`TestRejectionLeavesNoTrace`）；
- 20 组随机两两双向同步顺序下向量/日志/视图收敛
  （`TestConvergenceRandomOrders`）；
- 增量协议与“整份发送”朴素参照在相同调度下最终状态逐副本一致
  （`TestIncrementalMatchesNaiveReference`）；
- 多执行体并发写入与同步在竞态检测下安全并收敛
  （`TestConcurrentWritesAndSyncs`）；
- 决策日志包含目标向量、发送变更与判定依据（`TestDecisionLog`）。
