# NOTES

## 八步推演（key=k；链按 ver 新→旧；头节点=最新版本）

| # | 操作 | t | 链 | 结果 |
|---|---|---|---|---|
| 1 | Write(k,"v1") | 1 | [1] | 返回 1 |
| 2 | Snapshot() | 1 | [1] | A=1；活跃{1} |
| 3 | Write(k,"v2") | 2 | [2,1] | 返回 2；v1 原节点不动 |
| 4 | Snapshot() | 2 | [2,1] | B=2；活跃{1,2} |
| 5 | Write(k,"v3") | 3 | [3,2,1] | 返回 3 |
| 6 | Read(k,A=1) | 3 | [3,2,1] | 最大 ver≤1 = 1 → "v1" |
| 7 | Read(k,B=2) | 3 | [3,2,1] | 最大 ver≤2 = 2 → "v2" |
| 8 | Release(A); Collect() | 3 | [3,2] | v1 区间 [1,2) 无活跃快照→回收；v2 区间 [2,3) 含 B→保留；回收数 1 |

- (甲) 可见性若误写成 `ver < s`：ver1<1 不成立，链上无可见版本 → found=false（正确为 "v1"）。
- (乙) 若原地覆盖（v1 节点改成 v2、不建新节点）：ver1 处已是 "v2"，第 6 步 Read(k,A) 错返回 "v2"。
- (丙) 若 Collect 回收所有非头版本：只剩 v3，Read(k,B=2) 无 ver≤2 → found=false（正确为 "v2"）。

## 四条不变量：保证位置 / 钉住的测试

1. 快照隔离：COW 在 `chain.go` 的 `(*Chain).Prepend`（只追加新节点，旧节点不改）；`mvcc.go` 的 `Read` 仅按入参 s 经 `Visible` 二分。测试：TestSnapshotIsolation、TestConcurrentOldSnapshotReaders。
2. 朴素参照一致：`chain.go` 的 `Visible` 保留全部节点、二分取最大 ver≤s。测试：TestNaiveReference。
3. 回收安全：`chain.go` 的 `Collect` 对每个非头节点查 [v,v′) 内是否存在活跃快照，无则删，头节点永留。测试：TestCollectSafety。
4. 失败不留痕：`mvcc.go` 的 Write/Read/Release 先校验（空 key / s<0 或 s>t / 快照未激活）再改任何状态。测试：TestRejectedOpsLeaveNoTrace。
