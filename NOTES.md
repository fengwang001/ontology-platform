# ontology-634 缓冲池 NOTES

## 八步推导（numFrames=3，格式 pageId/dirty/pin，c=clean d=dirty，-=空帧）

| 步 | 操作 | 返回/动作 | f0 | f1 | f2 | writes |
|---|---|---|---|---|---|---|
| 1 | Pin(1) | f0 | 1/c/1 | - | - | 0 |
| 2 | Pin(2) | f1 | 1/c/1 | 2/c/1 | - | 0 |
| 3 | Pin(3) | f2 | 1/c/1 | 2/c/1 | 3/c/1 | 0 |
| 4 | MarkDirty(0) | f0 置脏 | 1/d/1 | 2/c/1 | 3/c/1 | 0 |
| 5 | Unpin(0) | f0 pin-- | 1/d/0 | 2/c/1 | 3/c/1 | 0 |
| 6 | Pin(4) | 驱逐 f0（脏→写回）装 4，返回 f0 | 4/c/1 | 2/c/1 | 3/c/1 | 1 |
| 7 | Unpin(1) | f1 pin-- | 4/c/1 | 2/c/0 | 3/c/1 | 1 |
| 8 | Pin(5) | 驱逐 f1（clean 不写回）装 5，返回 f1 | 4/c/1 | 5/c/1 | 3/c/1 | 1 |

(甲) 若不查 pin 误驱逐 f1：page2 的持有者仍认定它驻留 f1，此后读到/写入的却是 page4 的数据（张冠李戴），page2 未写回的修改静默丢失，page2 之后还可能被再次装入造成重复驻留。
(乙) 第 6 步正确写回后 writes=1。若驱逐脏页不写回直接覆盖，page1 的修改永久丢失，且 writes 停在 0，连「丢过数据」这一事实都观测不到。
(丙) 不检测 pin==0 时，对同一帧连续两次 Unpin 后 pin=-1；该帧仍被当作可驱逐帧，之后装入新页会误驱逐仍被其他使用者占用的页（使用中的页被覆盖、数据错乱）。

## 四条不变量：保证位置与钉住测试

1. 唯一性/守恒：pool.Pin 先查 loc 命中即复用，装入新页前 delete 旧映射（pool/pool.go Pin）；测试 TestConcurrentPin、checkRandom（api/api.go）。
2. 与朴素参照一致：api.SelfCheck 对内置序列逐步比对快照与 writes（api/api.go）；测试 TestSelfCheck、TestNaiveConsistency。
3. pin 安全：驱逐候选仅 frame.Evictable（Pin==0，frame/frame.go），pool 只在可驱逐帧上驱逐；测试 TestPinSafety。
4. 失败不留痕：pool 中全部校验先于任何状态修改（pool/pool.go Pin/Unpin/MarkDirty）；测试 TestFailureNoTrace、TestFaultInjection。
