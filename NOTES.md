# NOTES — ontology-430 跨分区会话键归并

## 第三节：八步推导（"-" 表示未冻结）

| 步 | 操作 | s1 已见 Seq（升序） | s1 结果 | s2 已见 Seq | s2 结果 |
|---|---|---|---|---|---|
| 1 | A("s1",2,1) | {2} | - | {} | - |
| 2 | A("s2",1,3) | {2} | - | {1} | - |
| 3 | A("s1",1,5) | {1,2} | - | {1} | - |
| 4 | A("s1",4,8) | {1,2,4} | - | {1} | - |
| 5 | A("s1",3,2) | {1,2,3,4} | - | {1} | - |
| 6 | C("s1",4) | {1,2,3,4} | 5128（关闭） | {1} | - |
| 7 | A("s1",5,9) | {1,2,3,4} | 5128（拒绝不留痕） | {1} | - |
| 8 | C("s2",1) | {1,2,3,4} | 5128 | {1} | 3（关闭） |

- (甲) 第 6 步后 s1 正确 **5128**（按 Seq 升序 5,1,2,8）；按到达序拼 1,5,8,2 错成 **1582**。
- (乙) 归并键误写成 Seq：第 3 步 seq1 槽已被 s2 的 3 占用，s1 的 5 撞键——报误冲突则 s1 永缺 seq1、第 6 步也失败；槽被覆盖则第 8 步 C("s2",1) 错读到 **5**（正确 3）。
- (丙) 第 7 步 s1 已关闭：返回 ErrClosed 整体拒绝不留痕，集合仍 {1..4}、结果仍 5128；不检查关闭直接拼接则 5128*10+9 错成 **51289**。

## 第二节：四条不变量（代码位置 / 钉住的测试函数）

1. 与批量重算一致：折叠只在 `seg.Close` 按 seq=1..n 计算 `r=r*10+v`（seg/seg.go 的 Close）；TestBatchConsistency、TestSegTable 钉住。
2. 结果冻结不变：Append 与异 N Close 在 seg 顶部 closed 分支拒绝，result 仅关闭成功时写一次；TestSegTable、TestEightSteps、TestConcurrentAppend 钉住。
3. 唯一性/幂等：seg.Append 命中同 seq 时同值返回 nil、异值 ErrConflict 且不写 map；TestSegTable、TestErrorsDistinctNoTrace、TestBatchConsistency 钉住。
4. 失败不留痕：seg 全部拒绝在写 map/字段前 return，mrg 新键先在临时 seg 判定成功才入表；TestNewSessionRejectNoTrace、TestErrorsDistinctNoTrace、TestSegTable 钉住。
