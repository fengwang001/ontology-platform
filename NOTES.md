# 粘滞会话单调读 — 推导与不变量

## 一、第三节推导：五行分步表

前置状态：R0/R1/R2 的 applied = 5/3/2，k 值 = E/C/B；会话 S 粘滞 R0，seen=0。

| # | 操作 | seen 前→后 | catch-up 补的 lsn | 目标副本 applied 前→后 | Read 返回 |
|---|------|-----------|-------------------|------------------------|-----------|
| 1 | Read(S,k) | 0→5 | 无（R0 已 5≥0） | R0: 5→5 | (E, 5) |
| 2 | SwitchReplica(S,R1) | 5→5 | {4,5}（D,E） | R1: 3→5 | — |
| 3 | Read(S,k) | 5→5 | 无 | R1: 5→5 | (E, 5) |
| 4 | SwitchReplica(S,R2) | 5→5 | {3,4,5}（C,D,E） | R2: 2→5 | — |
| 5 | Read(S,k) | 5→5 | 无 | R2: 5→5 | (E, 5) |

- (甲) 不做 catch-up：第 4 行后 R2.applied 仍为 2，第 5 行 Read 返回 **(B, 2)**；正确应为 **(E, 5)**。lsn 5→2 回退，**违反单调读**。
- (乙) 不粘滞、轮询直读：读到 R0 的 E(5) 后，第二次轮到 R1（applied=3）会读到 **C(3)**；单调读应读到 **E(5)**。
- (丙) 开区间漏掉 lsn=seen：切 R1 只补 {4}，R1.applied=4、k=D，Read 返回 **(D, 4)**；正确应为 **(E, 5)**（lsn 5→4 同样回退）。

## 二、四条不变量的保证位置与钉住它的测试

1. 单调读：api.Read 中 `seen=max(seen,applied)` 且先补全再读（api.go）；TestMonotonicReads。
2. 与朴素参照一致：View 由日志全量重放得出，Read 只读已补全副本（api.go）；TestNaiveReference。
3. 补全到位：SwitchReplica 先 CatchUp(seen) 再改绑定（api.go）；TestSwitchCaughtUp。
4. 失败不留痕：所有校验先于任何变更，哨兵错误直接返回（api.go）；TestFailureNoTrace。
