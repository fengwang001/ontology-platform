# snapshotfs —— 写时复制快照空间账本

`snapshotfs.Ledger` 是并发安全的 COW 快照空间账本：每个块只记录
**出生事务号 `b`** 与 **死亡事务号 `d`**（`d` 为无穷表示现存/存活），
据此即可判定它被哪些快照持有，精确计算总占用、每快照引用量、销毁与
回滚真正释放/复活的字节。

## 存活区间与快照包含关系

- `cur` 初始为 `1`。`Alloc(size)` 成功创建块，`b = cur`，`d = ∞`；
  块编号从 1 起连续，仅成功分配才消耗编号。
- `Free(id)` 把存活块的 `d` 置为 `cur`。
- 块在事务号 `t` 存活当且仅当 **`b ≤ t < d`**（左闭右开）。
- `Snapshot(name)` 在 `t = cur` 建立快照，随后 `cur` 加 1；
  快照 `s`（事务号 `t_s`）包含块当且仅当该块在 `t_s` 存活。

由此得到两个关键边界（均有测试覆盖）：

- 同一事务号内先 `Free` 再 `Snapshot`：`d == t_s`，块**不含**于该快照；
  先 `Snapshot`（`cur` 已加 1）再 `Free`：`d > t_s`，块**仍含**。
- 死亡号恰等于后一快照事务号时不被后者包含；小于时也不含。

## Used 与 Unique

- 块被**持有**当且仅当 `d == ∞`（现存），或存在包含它的未销毁快照。
- `Used` = 全部被持有块的大小之和；`Alloc` 要求 `Used + size ≤ Cap`
  （恰等允许）。
- `Referenced(name)` = 该快照所含全部块的大小之和。
- `Unique(name)` = **此刻销毁该快照将释放的字节数**：`d` 有限、且包含
  它的未销毁快照只有该快照一个的块大小之和。现存块（`d == ∞`）不计。

不变量：任意时刻 `Used` 等于逐块按定义重算的结果且 `≤ Cap`；
各快照 `Unique` 之和 `≤ Used − 现存块字节`；销毁全部快照后
`Used` 恰等于现存块字节。

## 持有计数

- `Hold(name)` 加 1，`Release(name)` 减 1；计数为 0 时 `Release` 报
  `ErrNotHeld`。持有计数大于 0 的快照不得被 `Destroy`。

## 回滚

`Rollback(name)` 原子完成以下操作，`cur` 不变：

1. 销毁事务号 `t > t_name` 的全部未销毁快照；
2. 凡 `b > t_name` 的块一律**丢弃**（无论存活；编号视为已丢弃，不再占用，
   之后 `Free` 报 `ErrDiscarded`，区别于从未分配的 `ErrNotFound`）；
3. 凡 `b ≤ t_name < d` 的块（即目标快照包含的块）`d` 恢复为 `∞`
   （**复活**，之后可再次 `Free`）；
4. 其余块与快照不动。

`name` 自身被持有**不**阻止回滚；但将被第 1 步销毁的任一日后快照
（不含 `name` 自身）持有计数大于 0 时，整体报 `ErrHeld` 且状态不变。
回滚不会使 `Used` 增加。已销毁快照的名字可重新用于新快照。

## 拒绝顺序（只报第一个，且拒绝不改变任何状态）

错误哨兵均可通过 `errors.Is` 区分：

| 错误 | 含义 |
| --- | --- |
| `ErrInvalidArgument` | `Cap ∉ [1, 2^50]`；`size ∉ [1, 2^40]`；快照名为空；`Free(id)` 的 `id < 1` |
| `ErrOutOfSpace` | `Used + size > Cap` |
| `ErrNotFound` | `Free` 的编号大于已成功分配的最大编号 |
| `ErrDiscarded` | 编号已被回滚丢弃 |
| `ErrDead` | 块已经 `Free` 过且未被回滚复活 |
| `ErrSnapshotExists` | 快照重名 |
| `ErrSnapshotMissing` | `Destroy/Referenced/Unique/Hold/Release/Rollback` 的名字不存在 |
| `ErrNotHeld` | `Release` 时持有计数为 0 |
| `ErrHeld` | `Destroy` 被持有的快照，或 `Rollback` 将销毁的某日后快照被持有 |

判定顺序：参数非法 → 空间不足 → 编号（不存在 → 已丢弃 → 已死亡）→
快照重名 → 快照不存在 → 未持有 / 被持有。

## 并发

全部方法在单个读写锁下串行化状态变更（查询用读锁），结果等价于某个
串行顺序，`Rollback` 对并发查询原子可见。

## 本地验证

```bash
# 全量测试
go test ./...

# 竞态检测（含并发压力用例）
go test -race -count=1 ./snapshotfs

# 查看随机对照的输入/输出/判定依据日志（2000 组）
go test -v -run TestRandomVsModel ./snapshotfs

# 代码检查
gofmt -l .
go vet ./...
```

`TestRandomVsModel` 以固定种子生成 2000 组随机操作序列，用严格按定义
逐块重算的朴素模型对照每个操作的错误与返回值、每步的 `Used/Cur/
Referenced/Unique` 及全部不变量，并在独立账本上重放记录，确认相同
操作序列得到完全相同的结果（确定性）。
