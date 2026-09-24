# NOTES

## 九步推导（Q=队列，S=Sum，L=Last，Snap=快照，CS=通道状态，R=记录中；初始 Q 空、S=(0,0)、L=(—,—)）

| # | 操作 | Q1 | Q2 | S | L | Snap(S,L) | CS1 | CS2 | R |
|---|---|---|---|---|---|---|---|---|---|
| 1 | Arrive(2,5) | [] | [5] | (0,0) | (—,—) | — | — | — | — |
| 2 | Step(2) | [] | [] | (0,5) | (—,5) | — | — | — | — |
| 3 | Arrive(2,2) | [] | [2] | (0,5) | (—,5) | — | — | — | — |
| 4 | Arrive(1,4) | [4] | [2] | (0,5) | (—,5) | — | — | — | — |
| 5 | Barrier(1,1) | [4] | [2] | (0,5) | (—,5) | S=(0,5) L=(—,5) | [4] | [2] | ch2 |
| 6 | Step(2) | [4] | [] | (0,7) | (—,2) | 同上 | [4] | [2] | ch2 |
| 7 | Arrive(2,6) | [4] | [6] | (0,7) | (—,2) | 同上 | [4] | [2,6] | ch2 |
| 8 | Arrive(1,7) | [4,7] | [6] | (0,7) | (—,2) | 同上 | [4] | [2,6] | ch2 |
| 9 | Barrier(2,1) | [4,7] | [6] | (0,7) | (—,2) | 检查点1完成 | [4] | [2,6] | 无 |

Restore：S=(0,5)、L=(—,5)；Q1=CS1+屏障后=[4,7]，Q2=[2,6]。RunAll 后 **S=(11,13)、L=(7,6)**（与不崩溃直接 RunAll 相同）。

- (甲) 6 在 ch2 收屏障前到达，属检查点 1，应进 CS2。漏存实现恢复后 Q2=[2]：Sum2=7（应 13）、Last2=2（应 6）。
- (乙) 2 该留在 CS2（快照 S 不含它）；5 不该存（已入快照 S）。剔除快照后处理掉的记录→恢复 Sum2=11；把屏障前全部到达都存（含已处理的 5）→恢复 Sum2=18。
- (丙) 重放顺序颠倒（先屏障后、再 CS）→ Q1=[7,4]、Q2=[6,2]：Last1=4（应 7）、Last2=2（应 6），Sum 不变。对齐式参照快照：S=(4,13)、L=(4,6)，CS 0 条。

## 不变量与保证位置 / 钉住测试

1. 对齐一致：uck.Barrier 首屏障拍快照且 CS=各队列、记录中到达追加进 CS（uck.go Barrier/Arrive）→ api_test.TestAlignedExactlyOnce
2. 恰好一次：CS 只含「快照时未处理」+「记录中到达」，Step 不删 CS（uck.go Step / chq SnapshotState）→ api_test.TestAlignedExactlyOnce
3. 恢复等价：uck.Restore 用 cSum/cState/cPost 重建队列（uck.go Restore）→ api_test.TestRestoreEquivalence
4. 失败不留痕：uck 各操作先校验后变更（uck.go Arrive/Step/Barrier 开头）→ api_test.TestRejectNoMutation
