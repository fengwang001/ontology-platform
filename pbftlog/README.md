# PBFT 固定视图副本日志（`pbftlog`）

在一个**固定视图** `v` 内维护单个 PBFT 副本的请求日志：收集
`PrePrepare / Prepare / Commit` 三类消息，按两级证书条件判定，并按序号
严格升序执行。不建模视图变更与检查点。

## 参数与角色

- 副本总数 `N = 3f+1`，编号 `0..N-1`，要求 `f >= 1`。
- 固定视图 `v >= 0`，主节点 `primary = v mod N`。
- 窗口大小 `L >= 1`；已执行序号 `executed` 初始为 `0`。
- 序号 `s` 在窗口内当且仅当 `executed < s <= executed+L`。
- 构造：`New(f, view, limit) (*Log, error)`，参数非法时分别返回
  `ErrInvalidFaultBound`、`ErrInvalidView`、`ErrInvalidLimit`。

## 消息与记录规则

消息统一为 `Message{View, Seq, Digest, From}`，本副本自发消息也走同一入口
`Handle(kind, m)`。相同内容（同一 `from`、同一 `(v,s,d)`）重复记录是合法
空操作；拒绝不改变任何状态（先完成全部校验，再写状态）。

- **PrePrepare**：`from` 必须是主节点；同一 `(v,s)` 已有不同摘要的预准备则
  拒绝（`ErrConflictingPrePrepare`）。
- **Prepare**：`from` 不得是主节点；同一 `from` 对同一 `(v,s)` 已记录不同
  摘要则拒绝（`ErrConflictingPrepare`）。
- **Commit**：任意副本可发；同一 `from` 对同一 `(v,s)` 已记录不同摘要则
  拒绝（`ErrConflictingCommit`）。
- Commit 可以先于 PrePrepare/Prepare 到达，后续补齐后证书自动成立。

## 两级证书

设某个序号已记录的预准备摘要为 `d`。

- **Prepared(s,d)**：存在摘要为 `d` 的预准备，且至少 `2f` 个**互不相同的
  非主节点**对 `(v,s,d)` 发出过 Prepare（主节点的 Prepare 被角色规则拒绝，
  天然不计；摘要不一致的 Prepare 不计；同一副本重复只计一次）。
- **CommittedLocal(s,d)**：Prepared(s,d) 成立，且至少 `2f+1` 个**互不相同
  的副本（含主节点）**对 `(v,s,d)` 发出过 Commit。

判定 API：`Prepared(seq, digest)`、`CommittedLocal(seq, digest)`。

## 执行规则

`Execute()` 从 `executed+1` 起逐个检查：只要该序号存在使
CommittedLocal 成立的摘要（即其唯一预准备摘要满足两级证书），就执行、
`executed++` 并继续下一序号；遇到第一个不满足的序号立即停止。返回本次新
执行的 `[]ExecutedEntry{{Seq, Digest}}`（每次新分配切片，不与内部状态共享
底层数组）。因此：

- 每个序号恰好执行一次；序号 2 先就绪而序号 1 未就绪时不执行，序号 1
  就绪后的下一次 `Execute()` 一次返回 `[1,2]`。

## 窗口随执行推进

- 窗口下界随 `executed` 单调右移：已执行序号 `<= executed` 的迟到消息与超出
  `executed+L` 的消息一样被 `ErrSeqOutOfWindow` 拒绝。
- 执行序号 `s` 后其内部条目被删除，窗口滑动后 `s+L` 变为可接受。

## 错误优先级（只报第一个）

`Handle` 按以下固定顺序检查，命中第一个即返回对应的可区分哨兵错误：

1. `from` 越界（不在 `[0,N)`）— `ErrSenderOutOfRange`
2. 摘要为空 — `ErrEmptyDigest`
3. 视图不等于 `v` — `ErrWrongView`
4. 序号不在窗口（`s <= executed` 或 `s > executed+L`）— `ErrSeqOutOfWindow`
5. 角色违规：非主节点发 PrePrepare — `ErrPrePrepareFromBackup`；
   主节点发 Prepare — `ErrPrepareFromPrimary`
6. 表态冲突：预准备摘要冲突 — `ErrConflictingPrePrepare`；
   同一 `from` 的 Prepare/Commit 摘要冲突 —
   `ErrConflictingPrepare` / `ErrConflictingCommit`

所有错误都是 `pbftlog.RejectError`，可用 `errors.Is`/`errors.As` 判别。

## 并发与确定性

所有方法在单个互斥锁下线性化，可并发调用，结果等价于某个串行顺序。
只要最终被接受的消息集合相同（每类消息同一 key 无摘要冲突），任意到达
顺序（含 Commit 先到）产生相同的最终 `executed` 与执行序列。

## 不建模范围

- 不建模视图变更（view change）与主节点切换。
- 不建模检查点（checkpoint）、水位校正与稳定状态。
- 不建模垃圾回收之外的消息重传、批处理、客户端请求去重与签名验证。

## 本地验证

```bash
go test ./pbftlog
go test -race -v ./pbftlog          # 竞态检测 + 输入/输出/判定依据日志
go test -race -count=10 ./pbftlog   # 反复运行随机对照与并发用例
go vet ./...
```

测试覆盖：

- `f=1/f=2` 恰好 `2f` 个 Prepare 成立、少 1 个不成立；主节点 Prepare 被拒
  不计；摘要不一致不计；同副本重复只计一次。
- Commit 先到、Prepare 后到时，Prepared 成立前不得 CommittedLocal；恰好
  `2f+1` 个 Commit 成立、少 1 个不成立。
- `executed+L` 被接受、`executed+L+1` 被拒；已执行序号的迟到消息被拒。
- 序号 2 先就绪不执行，序号 1 就绪后一次按序执行 1、2；拒绝不改状态；
  错误优先级；返回切片不与内部状态别名。
- `TestDifferentialRandomSequences` 以独立的“逐序号计数”朴素实现为 oracle，
  对 400 组随机消息/执行交织逐操作对照拒绝原因、`executed` 与执行序列。
- `TestArrivalOrderInvariance` 对同一批消息做两种乱序送达，比较最终结果。
- `TestConcurrentHandleAndExecute` 并发投递全部消息并并发 `Execute`，
  验证每个序号恰好执行一次且严格升序。
