# NOTES — 变更流「序号↔位点」稀疏双索引

## 三、推导（segmentSize=3，每 3 条一个分段，分段首条记锚点）

| 步 | Seq | len | 起始Off | 结束Off | 新增锚点 |
|---|---|---|---|---|---|
| 1 | 1 | 10 | 0 | 10 | (1,0) |
| 2 | 2 | 15 | 10 | 25 | 无 |
| 3 | 5 | 10 | 25 | 35 | 无 |
| 4 | 9 | 20 | 35 | 55 | (9,35) |
| 5 | 10 | 5 | 55 | 60 | 无 |
| 6 | 20 | 20 | 60 | 80 | 无 |
| 7 | 21 | 20 | 80 | 100 | (21,80) |
| 8 | 22 | 20 | 100 | 120 | 无 |

(甲) `LocateSeq(10)`=**55**（取最后 Seq≤10 的锚点 (9,35)，从 35 起扫：9@35→10@55）。若错用「第一个 Seq≥s 的锚点」：取到 (21,80)，从 80 起只能扫到 Seq 21、22，永远扫不到 10 → 错报「不存在」，而非返回 55。
(乙) `LocateSeq(9)`=**35**（目标恰是锚点 (9,35)）。若锚点错记「分段首条的结束 Offset」：锚点变 (9,55)，`LocateSeq(9)` 错返回 **55**（实为 Seq=10 的起点），偏差恰为该事件长度 20。
(丙) `LocateSeq(15)`：15 在 [1,22] 内但从未出现 → 返回「不存在」哨兵。若错成「返回最后 Seq≤s 的已存在事件」，会错返回 Seq=10 的 Offset **55**。越界：s 早于首事件 Seq（<1）或晚于末事件 Seq（>22），Offset 同理（o<0 或 o≥120）；不存在：落在范围内、但该 Seq/起始 Offset 从未出现（稀疏空洞）。

## 二、四条不变量：保证位置 + 钉住测试

1. 与朴素参照一致：`index.LocateSeq/LocateOffset` 自锚点起线性扫描比对（index/index.go）；测试 `TestLocateMatchesNaive`。
2. 锚点自洽：`index.Append` 仅在「事件下标 % seg == 0」时记 (seq, 起始Off)，`index.Verify` 逐锚点重扫比对；测试 `TestAnchorPlacement`、`TestVerifyDetectsCorruption`。
3. 损坏可判定可恢复：`index.Verify` 报 `ErrCorrupt`，`index.Rebuild` 从事件流重建全部锚点；测试 `TestVerifyDetectsCorruption`。
4. 失败不留痕：`index.Append` 先校验（seq 正且严格递增、ln 正）再改状态，查询类只读；测试 `TestRejectedNoStateChange`、`TestErrorsDistinct`。
