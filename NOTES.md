# offset/effect 两阶段提交：推导与不变量
## 十三步轨迹（初始 C=0, store={}）
| # | 操作 | store | C | 结果 |
|---|---|---|---|---|
| 1 | Apply(1,10) | {1:10} | 0 | OK |
| 2 | Commit(1) | {1:10} | 1 | OK |
| 3 | Apply(2,20) | {1:10,2:20} | 1 | OK |
| 4 | Apply(2,20) | {1:10,2:20} | 1 | OK（覆盖，值不变） |
| 5 | Restart() | {1:10,2:20} | 1 | OK，Pending=[2] |
| 6 | Apply(2,20) | {1:10,2:20} | 1 | OK（崩溃后重投） |
| 7 | Commit(2) | {1:10,2:20} | 2 | OK |
| 8 | Commit(4) | {1:10,2:20} | 2 | ErrOffsetJump |
| 9 | Apply(3,30) | {1:10,2:20,3:30} | 2 | OK |
| 10 | Commit(3) | {1:10,2:20,3:30} | 3 | OK |
| 11 | Commit(4) | {1:10,2:20,3:30} | 3 | ErrEffectMissing |
| 12 | Apply(4,40) | {1:10,2:20,3:30,4:40} | 3 | OK |
| 13 | Commit(4) | {1:10,2:20,3:30,4:40} | 4 | OK |
甲：颠倒为「先位点后效果」且崩在 Commit(2) 与 Apply(2) 之间：重启后 C=2、store 无 2，事件 2（效果 20）永久丢失（C 声称 ≤2 已提交，永不再投递）。正确顺序同点崩溃：C=1、store[2]=20 仍在，Pending=[2]，事件 2 只会被重复处理。
乙：第 8 步返回 ErrOffsetJump、C 保持 2；若不检测直接批量提交则 C=4，而 store 无 3，事件 3（效果 30）被跳过、永远不再处理。
丙：累加语义下步 3/4/6 三次 =20+20+20=60（应为 20）；故至少一次语义必须配 seq 级幂等（覆盖/去重），重投不得累加。
## 四条不变量（保证位置 / 钉住测试）
1. 不丢（先效果后位点）：txn.go Commit 仅在 store[C+1] 存在时才推进 C，Restart 经 store.Clone() 保留全部已写效果 — TestRestartPending、TestThirteenStepSequence
2. 位点连续无跳跃：txn.go Commit 对 seq>C+1 直接拒、C 只做 +1，连续性只 Get(C+1) 一次 — TestCommitCheckCountO1、TestThirteenStepSequence
3. 幂等重放：eff.go Put 按 seq 覆盖、txn.go Apply 对 seq<=C 空操作；值只取决于最后一次 Apply — TestIdempotentReplayAgainstNaive
4. 失败不留痕：txn.go 四类拒绝分支均在 Put/赋值之前 return，store 与 C 不被触碰 — TestErrorsDistinctAndStateUntouched
