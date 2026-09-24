# NOTES

## 八步推导（链按版本号新→旧；A=1，B=2）

| 步 | 操作 | t | 链 ver | 结果 |
|---|---|---|---|---|
| 1 | Write(k,"v1") | 1 | [1] | 返回 1 |
| 2 | Snapshot() | 1 | [1] | A=1，活跃{1} |
| 3 | Write(k,"v2") | 2 | [2,1] | 返回 2；v1 节点原地不动 |
| 4 | Snapshot() | 2 | [2,1] | B=2，活跃{1,2} |
| 5 | Write(k,"v3") | 3 | [3,2,1] | 返回 3 |
| 6 | Read(k,A) | 3 | [3,2,1] | "v1"（最大 ver≤1） |
| 7 | Read(k,B) | 3 | [3,2,1] | "v2" |
| 8 | Release(A);Collect() | 3 | [3,2] | 仅回收 v1：v2 的 [2,3) 含 B=2 保留；v1 的 [1,2) 无活跃快照 |

- (甲) 可见性若写成 `ver < s`：A=1 时无 ver<1 → 错得 ("", found=false)。
- (乙) 若原地覆盖：ver1 节点内容被改成 v2 → Read(k,A) 错返 "v2"。
- (丙) 若回收所有非头版本：链只剩 [3=v3] → Read(k,B) 无 ver≤2，错得 ("", found=false)。

## 四条不变量：保证位置 / 钉住的测试

1. 快照隔离：版本节点不可变、Prepend 只插新头（chain.go `node`/`Prepend`），Read 只读旧节点（mvcc.go `Read`）— `TestSnapshotIsolation`
2. 与朴素参照一致：全量保留历史，`Visible` 二分取最大 ver≤s（chain.go `Visible`）— `TestNaiveReference`
3. 回收安全：仅当区间 [v,v′) 内无任何活跃快照才删非头节点，头节点跳过（mvcc.go `Collect` + chain.go `Collect`）— `TestCollectSafety`
4. 失败不留痕：api.go 所有校验先于任何状态修改，三类哨兵错误互不相同 — `TestRejectedOpsLeaveNoTrace`

复杂度：非导出计数器 `cmps` + `sort.Search` 二分（chain.go）— `TestComparisonSublinear`
并发：`sync.RWMutex`，旧快照节点永不被改写（mvcc.go）— `TestConcurrentOldSnapshot`
