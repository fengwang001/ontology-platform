# 多粒度意向锁层级管理器

`ontology.LockManager` 在由字符串命名的资源树（允许森林）上管理
IS、IX、S、SIX、X 五种模式的多粒度意向锁。所有方法均可并发调用，
由单一互斥锁保证结果等价于某个串行执行顺序。

## 强度偏序与 join

强度偏序为：

```
IS < IX < SIX < X
IS < S  < SIX < X
```

其中 **IX 与 S 不可比**。`join(a,b)` 是偏序下不小于二者的最小模式
（最小上界），关键一格为：

- `join(IX, S) = join(S, IX) = SIX`
- 可比的两个模式取较大者，如 `join(IS, X) = X`、`join(SIX, IX) = SIX`

一个事务在一个节点上至多持有一个模式；再次 `Lock` 时若已持 `h`，
目标模式为 `join(h, mode)`。

## 相容矩阵

两个不同事务在同一节点上可同时持有，当且仅当下列矩阵相容：

| 已持 \ 请求 | IS | IX | S | SIX | X |
| --- | --- | --- | --- | --- | --- |
| **IS**  | ✓ | ✓ | ✓ | ✓ | ✗ |
| **IX**  | ✓ | ✓ | ✗ | ✗ | ✗ |
| **S**   | ✓ | ✗ | ✓ | ✗ | ✗ |
| **SIX** | ✓ | ✗ | ✗ | ✗ | ✗ |
| **X**   | ✗ | ✗ | ✗ | ✗ | ✗ |

## 锁转换规则（Lock）

`Lock(txn, node, mode)` 按以下顺序处理：

1. 事务号必须为正整数；模式必须是五种之一；节点必须已登记。
2. 若该事务已持 `h` 且 `h >= mode`（已持模式不弱于请求模式），
   视为成功并直接返回 `h`，**不再检查祖先意向与冲突**，也不改任何状态。
3. 否则目标模式 `m = join(h, mode)`（未持有时 `m = mode`）。
4. 对 `node` 的每个真祖先（从根往下逐个检查），该事务在祖先上所持模式
   必须不小于所需意向，否则在第一个不满足的祖先处拒绝。
5. `m` 必须与其他每个事务在该节点上的模式相容，否则拒绝；
   冲突错误携带冲突事务中事务号最小者及其模式。
6. 全部通过后，该事务在该节点的模式置为 `m`。

注意第 4 步使用的是 **join 转换后的目标模式**，而不是请求模式：
例如已持 `S`、祖先只有 `IS` 时请求 `IX`，`join(S,IX)=SIX` 需要 `IX`
祖先意向，因此会被拒绝（而非按 `IX` 请求本身放过）。

## 祖先意向要求

| 目标模式 | 真祖先上所需意向 | 满足的模式 |
| --- | --- | --- |
| IS、S | IS | IS、IX、S、SIX、X（五种都满足） |
| IX、SIX、X | IX | IX、SIX、X（**S 不满足**） |

## 释放与查询

- `Unlock(txn, node)`：先校验事务号与节点，再要求该事务在该节点上持锁；
  若该事务在任意真后代上仍持锁则拒绝，以保证“持锁则祖先意向齐全”的不变量。
  错误携带（id 最小的）仍持锁的后代。
- `ReleaseAll(txn)`：在单个临界区内一次性释放该事务的全部锁，
  不暴露中间态；事务无持锁时同样成功。
- `Held(txn, node)`：返回 `(mode, held, err)`，未持有时 `held=false`。
- `Holders(node)`：按事务号升序返回 `[]Holder{{Txn, Mode}, ...}`。

## 错误

所有错误原因都可用 `errors.Is` 区分：

`ErrInvalidTxn`、`ErrInvalidMode`、`ErrEmptyNodeID`、`ErrNodeExists`、
`ErrParentNotExist`、`ErrNodeNotExist`、`ErrMissingIntent`、
`ErrLockConflict`、`ErrLockNotHeld`、`ErrDescendantLocked`。

结构化错误可取出关键字段：

- `*AncestorIntentError`（包装 `ErrMissingIntent`）：
  `Ancestor`（第一个不满足的祖先）、`Required`（所需意向）、`Held`。
- `*ConflictError`（包装 `ErrLockConflict`）：
  `ConflictTxn`（最小冲突事务号）、`ConflictMode`。
- `*DescendantLockError`（包装 `ErrDescendantLocked`）：
  `Descendant`、`Mode`。

被拒绝的操作不会改变节点树与任何持锁状态。

## 本地验证

```bash
# 全量测试（含 2000 组随机操作序列与朴素参考模型的逐步对拍）
go test ./...
go test -race -count=1 ./...

# 打印对拍的完整日志：输入、输出与每一步的判定依据
go test -run TestRandomDifferential -difflog -v

go vet ./...
gofmt -l .
```

针对性用例覆盖：S 与 IX 合并为 SIX、S 不满足 IX 意向、
转换按 join 后模式检查祖先、SIX 与 IS 相容而与 IX 冲突、
持强锁请求弱锁为无操作、Unlock 后代锁拒绝与 ReleaseAll 成功、
以及被拒绝后状态不变。
