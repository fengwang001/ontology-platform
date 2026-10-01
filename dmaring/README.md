# dmaring —— DMA 描述符环主机侧驱动模型

`dmaring` 用可精确复现的方式模拟 DMA 描述符环的所有权交接：主机按包提交
分散/聚集（scatter-gather）描述符，模拟设备按序处理（含出错连带丢弃），
主机按整包回收。

## 构造与环指针

- `New(N)`：槽数 `N` 必须是 2 的幂且 `N >= 2`，否则返回 `ErrBadN`。
- 三个指针只增、不回绕，记为无界整数，初值均为 0：
  - `prod`：下一个提交位（Submit 写入的起点）；
  - `dev` ：设备下一个处理位；
  - `reap`：主机下一个回收位。
  - 物理槽号恒为 `指针 % N`；指针可以远超 N，槽循环复用。
- 空闲数 `Free = N - (prod - reap)`；环可以被占满（Free 可以为 0），
  不留空槽。空闲数**只随 `Reap` 增加**：设备已处理但未回收的槽仍占用。
- 每个槽含：`OWN`（1 归设备）、`FIRST`、`LAST`、`Len`、`St`（初始 0）。
- 任意时刻成立：`reap <= dev <= prod`、`prod - reap <= N`、
  `OWN=1 的槽数 == prod - dev`。

## OWN 位与提交原子性

`Submit(lens)` 提交一个 `m = len(lens)` 段的包：

- 从 `prod` 起连续占用 `m` 个槽；首段 `FIRST=1`，末段 `LAST=1`，
  `m=1` 时同一槽两者都为 1；
- 各段写入 `OWN=1, st=0, len=lens[i]`，成功后 `prod += m`；
- 整个包**要么全部写入，要么不写**：任何校验失败都不改动指针、槽内容与
  故障登记。

校验按固定顺序只报第一个错误：
`表为空(ErrEmptyLens) -> 任一段 len<=0(ErrBadLen) -> m>N(ErrSegmentsOverN)
-> m>Free(ErrNoFreeSlots)`。

## 设备处理与出错连带丢弃

`DeviceRun(k)` 最多完成 `k` 个处理单位，返回实际完成数；`k<0` 返回
`ErrNegativeK`。设备从 `dev` 起逐槽处理：

- 遇到无描述符（`dev >= prod`）或 `OWN=0` 即停；
- 正常描述符为 1 个单位：处理后 `OWN=0, st=1`，`dev += 1`；
- 若该描述符的无界序号已被 `Fault` 登记：
  - 该描述符 `OWN=0, st=2`；
  - 同包其后各段（直到 `LAST`，含 `LAST`）一律 `OWN=0, st=3`，`dev`
    一并越过；若出错描述符自身就是 `LAST`，则没有连带段；
  - 这**整个连带丢弃只算 1 个处理单位**；同一次 `DeviceRun` 内剩余名额
    可继续处理其后的正常描述符。

`Fault(seq)` 登记无界序号 `seq` 在被处理时出错，要求 `seq >= dev`
（可 `>= prod`），否则返回 `ErrFaultBeforeDev`；重复登记同一序号无影响。
若某序号后来落在一次出错连带丢弃的段上（即被置为 `st=3`），该登记**作废**，
那一段保持 `st=3`，不会因为故障登记而变成 `st=2`。

## 整包回收

`Reap()` 从 `reap` 起取**一个完整包**（从 `FIRST` 扫到 `LAST`）：

- 先判无包：`reap == prod` 返回 `ErrNoPacket`；
- 再判完成：包内任一段 `OWN=1` 返回 `ErrPacketIncomplete`；
- 成功时返回 `ReapResult{Segments, TotalLen, FaultIndex}`：段数、各段
  `len` 之和、包内 `st=2` 那一段的包内下标（没有则为 -1），随后
  `reap += Segments`。

## 并发与可复现性

所有方法（提交、设备运行、故障登记、回收、`Free`/指针查询、`Snapshot`）
由同一把互斥锁串行化，可并发调用，结果等价于某个串行顺序。模型不含随机或
时钟因素：同一操作序列重放得到完全相同的槽内容、返回值与错误。

导出的可区分错误：`ErrBadN`、`ErrEmptyLens`、`ErrBadLen`、
`ErrSegmentsOverN`、`ErrNoFreeSlots`、`ErrNegativeK`、`ErrFaultBeforeDev`、
`ErrNoPacket`、`ErrPacketIncomplete`。

## 本地验证

```bash
go test -race -v ./dmaring/        # 全部用例（含朴素模拟差分与并发）
go test -run TestRandomDifferential -v ./dmaring/
go vet ./...
gofmt -l .
```

测试要点（用例内以 `IN/OUT ... 判断依据` 打印输入、输出与判定理由）：

- 段数恰等于 Free（占满）与比 Free 多一；段数等于 N 与大于 N；
- `m=1` 时 `FIRST` 与 `LAST` 同为 1；
- 设备处理后 `Free` 不增加，直到 `Reap`；
- 指针超过 N 后槽号取余复用（5 个完整周期后指针为 20）；
- 出错连带丢弃后同包末段为 `st=3`，`Reap` 报告正确的出错段包内下标；
- `Fault` 落在已被连带丢弃的段上作废；
- 出错连带在 `k` 中只占一个名额；
- 40 个固定随机种子、每种子 600 步操作与独立编写的逐步朴素模拟
  （`naive_test.go`）逐步对照返回值、错误、槽内容与三指针；
- 同一记录序列两次重放结果完全相同；
- 多 goroutine 并发调用下 `-race` 全程不变量成立。
