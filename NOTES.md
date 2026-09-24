# 快照+增量双读：推导与不变量

## 八步分步表（增量 = Seq>snapSeq 的日志）

| 步 | 操作 | Seq | snapSeq | snap | 增量 |
|---|---|---|---|---|---|
| 1 | Put(a,1) | 1 | 0 | {} | 1:Put a=1 |
| 2 | Put(b,2) | 2 | 0 | {} | 1:Put a=1, 2:Put b=2 |
| 3 | Snapshot | 2 | 2 | {a:1,b:2} | （空） |
| 4 | Put(a,5) | 3 | 2 | {a:1,b:2} | 3:Put a=5 |
| 5 | Del(b) | 4 | 2 | {a:1,b:2} | 3:Put a=5, 4:Del b |
| 6 | Put(c,9) | 5 | 2 | {a:1,b:2} | 3:Put a=5, 4:Del b, 5:Put c=9 |
| 7 | Snapshot | 5 | 5 | {a:5,c:9} | （空） |
| 8 | Put(a,7) | 6 | 5 | {a:5,c:9} | 6:Put a=7 |

- (甲) 终态下 `Read(a,6)`=**7**（基 a=5 + 增量 Seq6）；`Read(a,3)` 因 3<snapSeq=5 被**拒绝**。若按第 4–6 步的基（snapSeq=2）评 `Read(a,3)`=**5**。增量区间错写成 `Seq<atSeq`：`Read(a,6)` 错成 **5**（漏 Seq6），`Read(a,3)` 错成 **1**（漏 Seq3 取快照值）。
- (乙) 快照优先（错）：`Read(a,4)` 错成 **1**（对 5），`Read(a,6)` 错成 **5**（对 7）。
- (丙) tail-only：`Read(c,6)` 错成**不存在**（对 9），`Read(b,3)` 错成**不存在**（对 2）。分区无漏无重：快照覆盖 [1,snapSeq]、增量覆盖 (snapSeq,atSeq]，两者无交、并集恰为 [1,atSeq]。不变量 1 的参照必须「从空按 Seq 升序应用全部日志」，因为它只依赖日志、独立于快照；若写成「快照+增量直接拼」，等于用被测实现当自己判据，分区错了也查不出。

## 四条不变量：保证位置 + 钉住它的测试

1. 批量一致：`read.Merge` 以 snap 为基按 Seq 升序应用增量（read/read.go）；`TestBatchEquivalence`。
2. 交叠优先：增量在基之后应用、同键后者覆盖前者（read/read.go Merge 循环）；`TestOverlapPriority`。
3. 快照不变性：`Snapshot()` 只重算 base、不动日志（api/api.go Snapshot）；`TestSnapshotInvariance`。
4. 失败不留痕：校验先于任何状态变更（api/api.go Put/Del/Read 入口）；`TestFailureNoTrace`。
