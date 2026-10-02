# allocator — 按检查点纪元回收的数据源拆分分配器

`allocator` 把动态发现的拆分（split）分配给 `R` 个读取器，支持读取器
失败/重启、按检查点纪元的精确回收、反复失败拆分的隔离，以及精确的
「无更多拆分」信号。所有方法可并发调用，效果等价于某个串行顺序
（内部由一把互斥串行化，`ReaderFailed` 是原子步骤）。

## 纪元与检查点的对应关系

- 全局维护两个检查点号：`lt`（最近**触发**的检查点，初值 0）与
  `done`（最近**完成**的检查点，初值 0），恒有 `done <= lt`。
- **当前纪元 `E = lt + 1`**。`Checkpoint(cp)` 要求 `cp == lt+1`，触发后
  `lt` 加一，纪元随之加一；`Complete(cp)` 只推进 `done`，**不影响纪元**。
- 因此纪元按「触发」而非「完成」计：在 `Checkpoint(k)` 已触发、
  `Complete(k)` 尚未到达的窗口内，分配/完成发生在纪元 `k+1`……
  更准确地说，发生在纪元 `E = lt+1`；当 `Complete` 滞后时会出现
  `at <= lt` 但 `at > done` 的拆分，失败回收时它仍被归还（见下）。
- 每个拆分记录分配纪元 `at`（`RequestSplit` 时的 `E`）与完成纪元
  `ft`（`Finished` 时的 `E`）。

## 归还与撤销完成的判据推导

读取器失败后，它从最近**已完成**的检查点 `done` 恢复。恢复出的状态里
只包含 `done` 之前（含）持久化的分配与完成记录，据此对其名下每个
`ASSIGNED`/`FINISHED` 拆分（按编号升序处理）分类：

| 条件 | 含义 | 处理 |
| --- | --- | --- |
| `at > done` | 分配发生在恢复点之后，恢复状态里**没有**这条分配 | 归还：`ret+1`；`ret >= A` 则 `QUARANTINED`，否则 `UNASSIGNED`（完成状态一并丢弃） |
| `at <= done` 且 `FINISHED` 且 `ft > done` | 分配被保留，但完成记录未被持久化 | 撤销完成：回到 `ASSIGNED`，属主不变 |
| 其余（`at <= done` 且未完成，或 `ft <= done`） | 恢复状态与当前一致 | 保留不变 |

边界：`at == done` 保留、`at == done+1` 归还；`ft == done` 完成保留、
`ft == done+1` 撤销。保留在失败读取器名下的拆分不会被偷取、也不会
分配给任何人，直到该读取器 `ReaderRestarted`。

## 偷取与等待的规则

`RequestSplit(r)` 的候选选择：

1. 先取 `UNASSIGNED` 中偏好读取器（编号 mod R）等于 `r` 的编号最小者；
2. 没有则**偷取**：取 `UNASSIGNED` 中偏好读取器当前已失败的编号最小者
   （偏好读取器存活的拆分不参与偷取）；
3. 没有候选时：若已封口（`Seal`）且不存在任何 `UNASSIGNED` 与
   `ASSIGNED` 拆分（含失败读取器名下保留的），返回「无更多拆分」；
   否则返回「等待」。`FINISHED` 与 `QUARANTINED` 不阻止「无更多拆分」。

## 隔离次数的口径

- `ret`（被归还次数）**只在 `ReaderFailed` 归还拆分时加一**，其它操作
  不影响；构造参数 `A ∈ [1, 100]` 是隔离阈值。
- 归还时若 `ret >= A`，拆分进入 `QUARANTINED`（终态，不再分配，无属主）；
  差 1（`ret == A-1`）则回到 `UNASSIGNED` 池，可被再次分配。
- `ReaderFailed` 返回三个互不相交、按编号升序的清单：归还（回池）、
  隔离、撤销完成。

## 拒绝顺序与查询

每个操作只报第一个拒绝原因，且彼此可用 `errors.Is` 区分：
`AddSplits`（参数非法 → 已封口 → 编号重复，重复时整个列表不生效）、
`RequestSplit`（参数非法 → 读取器已失败）、`Finished`（参数非法 →
读取器已失败 → 不属于该读取器或非 ASSIGNED）、`ReaderFailed`
（参数非法 → 已失败）、`ReaderRestarted`（参数非法 → 未失败）、
`Checkpoint`（乱序）、`Complete`（参数非法 → 超前 → 过期）。
被拒绝的操作不改变任何状态。查询：`State(s)`、`Quarantined()`、
`Counts()`。

## 性能口径

- `probes`（非导出计数器）：`RequestSplit` 每次查看的候选堆顶数
  `<= R+1`，与未分配拆分总数无关（按偏好读取器分桶的索引最小堆，
  无惰性删除）。
- `owned`（非导出计数器）：`ReaderFailed` 逐个处理的拆分数，恰等于该
  读取器名下 `ASSIGNED + FINISHED` 数，与全部拆分总数无关。

## 本地验证

```bash
# 若 ~/.cache 只读，先指定构建缓存
export GOCACHE=/tmp/gocache

go test ./allocator/                 # 全部单元测试 + 2000 组随机对照
go test -race ./allocator/           # 竞态检测（含并发不变量测试）
go test -v -run TestRandomAgainstModel ./allocator/   # 查看输入/输出/判定日志
go test -v -run TestSpecExample ./allocator/          # 题目示例逐步重放
```
