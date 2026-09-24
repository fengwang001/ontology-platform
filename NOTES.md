# 快照+增量双读：八步推导与不变量

## 第三节：八步分步表（增量 = Seq>snapSeq 的日志）

| # | 操作 | Seq | snapSeq | snap | 增量列表 |
|---|------------|-----|---------|-------------|-------------------------------|
| 1 | Put(a,1)   | 1   | 0       | {}          | 1:Put a=1                     |
| 2 | Put(b,2)   | 2   | 0       | {}          | 1:Put a=1; 2:Put b=2          |
| 3 | Snapshot() | 2   | 2       | {a:1, b:2}  | （空）                        |
| 4 | Put(a,5)   | 3   | 2       | {a:1, b:2}  | 3:Put a=5                     |
| 5 | Del(b)     | 4   | 2       | {a:1, b:2}  | 3:Put a=5; 4:Del b            |
| 6 | Put(c,9)   | 5   | 2       | {a:1, b:2}  | 3:Put a=5; 4:Del b; 5:Put c=9 |
| 7 | Snapshot() | 5   | 5       | {a:5, c:9}  | （空）                        |
| 8 | Put(a,7)   | 6   | 5       | {a:5, c:9}  | 6:Put a=7                     |

- (甲) Read(a,6)=7（快照基 a=5 叠加增量 6:Put a=7）；Read(a,3)：3<snapSeq=5，拒绝 ErrSeqBeforeSnap。
  区间错成 Seq<atSeq：Read(a,6) 错成 5（漏掉 Seq=6 那条）；Read(a,3) 仍被拒（边界判定先于合并）。
- (乙) 正确：Read(a,4) 拒绝（4<5）、Read(a,6)=7。快照优先：Read(a,4) 仍被拒；Read(a,6) 错成 5（快照 a=5 盖住增量 a=7）。
- (丙) 正确：Read(c,6)=9、Read(b,3) 拒绝。tail-only：Read(c,6) 错成「不存在」（(5,6] 里只有 a）；
  Read(b,3) 若省掉边界检查则错成「不存在」（(5,3] 为空），而批量参照在 Seq=3 时 b=2，历史真值丢失。
  分区：快照覆盖 [1,snapSeq]、增量覆盖 (snapSeq,atSeq]，无交无缝，并集恰为 [1,atSeq]。
  批量参照必须「按 Seq 升序应用全部日志」：同键可同时落在快照与增量（a：快照 5、增量 7），
  「快照+增量直接拼」未定义冲突方向、结果依赖拼接顺序；全序重放+最新写胜出才唯一确定判据。

## 第二节：四条不变量（保证位置 → 钉住它的测试）

1. 与批量重算一致：read.go Read 用 View 取快照基、切 (snapSeq,atSeq] 增量按 Seq 升序应用 → TestDualReadMatchesBatch
2. 交叠优先级：read.go 合并循环中增量在快照基之后应用，Put 覆盖/Del 删除，Seq 大者胜 → TestDualReadMatchesBatch（eight-steps 用例）
3. 快照不变性：snap.go Snapshot 只重建前缀等价物且日志保留；View 单读锁内一致取三元组 → TestSnapshotInvariance
4. 失败不留痕：snap.go Put/Del 先校验后改态；read.go 先判 atSeq>=snapSeq 再合并 → TestFailureLeavesNoTrace
