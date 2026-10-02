# aries — ARIES 风格撤销阶段模型

`aries` 包实现了一个 ARIES 风格的事务回滚与崩溃重启撤销（undo）阶段模型：
沿 `prevLSN` 链撤销更新并写补偿记录（CLR），支持保存点部分回滚、按 LSN
从大到小跨事务的重启撤销，以及可中断、可续做的 `RestartStep`。

## 日志与记录

- 日志容量上限为 `Lmax`（1..10^6），LSN 从 1 起，每追加一条记录加一。
- 记录类型：
  - `U` 更新：事务、页、增量 `d`（非 0，|d| ≤ 10^9）；
  - `C` 补偿记录（CLR）：事务、页、增量、`undoNext`；
  - `K` 提交；`E` 结束。
- 每条记录携带 **prevLSN**：追加时刻该事务的 `lastLSN`（该事务最近一条
  记录的 LSN，无则 0）。`prevLSN` 把同一事务的全部记录串成一条链。
- `C` 的 **undoNext** 指向被它撤销的那条 `U` 的 `prevLSN`，即"撤销沿链
  继续前进的下一站"。它让撤销过程跳过已被补偿的区间，保证每条 `U`
  至多被一条 `C` 撤销，且 `undoNext` 严格小于自己的 LSN。
- 页值初值为 0；追加 `U`/`C` 时立即把增量加到页上（重做假定已完成，
  本模型只处理撤销）。任意时刻每页的值等于日志中全部 `U`/`C` 增量之和。

## next(t) 与跳转规则

```
next(t) = undoNext(lastLSN)  若 lastLSN 指向一条 C
next(t) = lastLSN            否则（lastLSN 为 0 时 next 为 0）
```

回滚/重启撤销从 `q = next(t)` 起循环，直到 `q <= s`（回滚）或 `q == 0`
（败者）：

- `q` 是 `U`：追加一条 `C`（增量取反、`undoNext = U.prevLSN`、页加上
  相反数），`q` 前移为 `U.prevLSN`；
- `q` 是 `C`：**只跳转、不写记录**，`q` 改为该 `C` 的 `undoNext`。

因此当 `next(t)` 指向一条 `C` 时，第一步只跳转；补偿记录自身永不被再撤销。

## 操作语义

- `Begin(t)` 登记事务（事务号不可复用，含已终止者）。
- `Update(t,p,d)` 追加 `U` 并立即改页。
- `Save(t)` 返回当前 `lastLSN` 作为保存点（可为 0），不写日志。
- `Commit(t)` 追加 `K` 并终止事务。
- `Rollback(t,s)` 撤销到保存点 `s`（`s` 为 0 或 `t` 的某条记录的 LSN）；
  保存点恰好等于某条记录的 LSN 时该记录**不**被撤销；`s == next(t)`
  时追加 0 条记录并成功。
- `Abort(t)` 等价于 `Rollback(t,0)` 后追加 `E` 并终止事务。
- `Rollback`/`Abort` 需要 `k`/`k+1` 条日志空间，不足时**整体拒绝**：
  不追加任何记录、不改任何页、不改事务状态。

## 崩溃与重启撤销

`Crash()` 使所有活跃事务成为败者，此后只接受 `Restart`、`RestartStep`
与查询。撤销阶段（`Restart`）的全局处理次序：

1. **初始 E 扫描**：按事务号升序，为 `next` 已为 0 且尚无 `E` 的败者
   各追加一条 `E`；
2. **主循环**：在 `next > 0` 的败者中取 `next` 最大者处理其 `q`
   （`U` 则追加 `C` 并前移，`C` 则只跳转）；任一败者的 `next` 变为 0
   时**立即**为其追加 `E`（`E` 排在使它归零的那条 `C` 之后；若归零来自
   跳转，则就在跳转之时追加）；
3. 全部败者结束后系统恢复接受正常操作；没有败者时 `Restart` 追加 0 条
   并直接恢复。`Restart`/`RestartStep` 不受 `Lmax` 限制。

**可中断续做**：`RestartStep(n)`（n ≥ 1）在本次调用追加满 `n` 条记录
（`C` 或 `E`）后立即返回；使 `next` 归零的那条 `C` 之后的 `E` 即使因
`n` 用完而跨到下一次调用，也必须先于任何其它记录追加。任意拆成多次
`RestartStep` 再以 `Restart` 收尾，所得日志与一次 `Restart` 逐条相同。
实现上通过 `restartState`（各败者的 `next` 指针、初始扫描游标、待写
`E` 队列）在调用间保存现场，保证续做位置精确。

## 拒绝顺序

每个操作按以下顺序只报第一个错误（`Error.Code`）：

1. `ErrInvalidParam` 参数非法（事务号/页号/`d`/`Lmax` 越界、`n < 1`）；
2. `ErrCrashed` 系统已崩溃（普通操作）；`Restart`/`RestartStep` 在未崩溃
   时报 `ErrNotCrashed`（仅次于参数非法）；
3. `ErrTxnExists`（仅 `Begin`）/ `ErrTxnNotFound`；
4. `ErrTxnTerminated`；
5. `ErrInvalidSavepoint`；
6. `ErrLogFull`。

被拒绝的操作不改变日志、页值与事务状态。

## 并发

所有操作与查询共用一把互斥锁，可并发调用，结果等价于某个串行顺序。

## 本地验证

```bash
# 全部单元测试 + 2000 组随机序列与朴素模拟的差分对照
go test ./aries/

# 打印每组随机序列的输入、输出与判定依据
go test ./aries/ -run TestDifferentialFuzz -v

# 竞态检测
go test ./aries/ -race
```

差分测试中的朴素模拟（`aries/model_test.go`）是对题述规则的另一种独立
实现：其重启撤销一次性预生成全部剩余记录再逐条消费，与生产实现的
增量式可续做状态机互为对照；每个操作序列还会在全新系统上重放一次，
验证日志与页值可精确复现。
