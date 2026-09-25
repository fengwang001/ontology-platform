# NOTES

## 推导（建 1→0,2→1,3→1,4→2,5→2,6→3,7→4,8→0,9→8,10→99）

Delete(1) 后序逐个加入顺序（七行表）：

| 步 | 加入 | 其父是否已在此前加入 |
|---|---|---|
| 1 | 7 | 否（父4未加入，子先父后） |
| 2 | 4 | 否（父2未加入） |
| 3 | 5 | 否（父2未加入；4、5 兄弟升序） |
| 4 | 2 | 否（父1未加入） |
| 5 | 6 | 否（父3未加入） |
| 6 | 3 | 否（父1未加入；2、3 升序） |
| 7 | 1 | 根 Parent=0，最后加入 |

此时 Orphans() = [10]（8→0、9→8 完好）。

(甲) Delete(2) 正确为 [7 4 5 2]；前序错成 [2 4 7 5]：消费者先删 2，则 4、5 瞬间悬挂，违反不变量 2（连带破坏 3）。
(乙) 只删直接子代会漏 4、5、6、7；删后 4、5、6 立即成孤儿（7 的父 4 尚存活，CleanupOrphans 时随 4 一并级联）。
(丙) Orphans() 正确为 [10]；把 Parent==0 的根也当孤儿会多报 1、8（Delete(1) 之后只剩根 8，即多报 8）。

## 不变量落点（代码位置 / 钉住的测试）

1. 与批量重算一致：casc.go `postorder` 沿 children 邻接表求传递闭包；测试 `TestDeleteMatchesBatch`。
2. 子先父后、兄弟升序：casc.go `postorder` 先递归有序 children 再 append 自己；测试 `TestDeleteOrder`。
3. 删除后无悬挂引用：casc.go `eraseLocked` 整子树删除并清理邻接桶；测试 `TestReferentialIntegrity`。
4. 失败不留痕：casc.go `Add` 全部校验先于 map 写入，api.go 仅转调；测试 `TestRejectedOpsNoTrace`。

另：非导出计数器 `lastTraversed` 仅在 Delete 记 DFS 节点数，由 `TestComplexityBounded` 钉住；并发由 `TestConcurrentReaders` 钉住；自检由 `TestSelfCheck` 钉住。
