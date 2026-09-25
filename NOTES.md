# NOTES：列式物化视图

格式：列写 a/b/c（按槽）；rowID1..4 = 0..3；slotOf 下标是 rowID。

| 步 | a | b | c | alive | slotOf | 返回/结果 |
|---|---|---|---|---|---|---|
| 1 Insert(10,20,30) | [10] | [20] | [30] | [T] | [0] | rowID=0 |
| 2 Insert(1,2,3) | [10,1] | [20,2] | [30,3] | [T,T] | [0,1] | rowID=1 |
| 3 Insert(100,200,300) | [10,1,100] | [20,2,200] | [30,3,300] | [T,T,T] | [0,1,2] | rowID=2 |
| 4 Delete(rowID1=0) | [10,1,100] | [20,2,200] | [30,3,300] | [F,T,T] | [-1,1,2] | 无返回，列值保留 |
| 5 Insert(7,8,9) | [10,1,100,7] | [20,2,200,8] | [30,3,300,9] | [F,T,T,T] | [-1,1,2,3] | rowID=3；新槽在末尾，槽0不复用 |
| 6 Compact() | [1,100,7] | [2,200,8] | [3,300,9] | [T,T,T] | [-1,0,1,2] | 存活槽保序重排，slotOf 重建 |
| 7 Update(rowID2=1,C,999) | [1,100,7] | [2,200,8] | [999,300,9] | [T,T,T] | [-1,0,1,2] | 改到 rowID=1 的槽0 |
| 8 Get(rowID2=1) | 同7 | | | | | (1,2,999) |

- (甲) 正确：改到第2行（rowID=1，原值 1,2,3），Get(rowID2)=(1,2,999)。若 Compact 不重建 slotOf（id1 仍指旧槽1，压缩后槽1装的是第3行 100,200,300），更新把 c[1] 改成 999（c=[3,999,9]），Get(rowID2) 读槽1，错成 **(100,200,999)**。
- (乙) 正确 Project([B]) = **[2,200]**（槽0墓碑跳过）。若不跳墓碑直接返回整列，得 [20,2,200]，多出已删第1行的 **20**。
- (丙) 正确：返回哨兵错误 ErrDeleted（已删行）。若只置 alive=false 而 slotOf[0] 未置 -1、Get 又不查 alive，会沿槽0读回残留列值，错返回 **(10,20,30)**（僵尸行）。

## 不变量落点

1. 与行式参照一致：每次变更只动单槽，Compact 保序搬移；`api.(*API).SelfCheck` 内置序列逐行对照朴素 map 模型。钉于测试 `TestRandomOpsMatchRowStore`、`TestSelfCheck`（api/api_test.go）。
2. 稳定 rowID：`col.(*Store).Delete` 只置墓碑/列值不动，Insert 恒 append 末尾槽，`col.(*Store).Compact` 重建 slotOf 并逐槽拷贝存活值。钉于 `TestCompactPreservesBindings`、`TestEightStepScenario`。
3. 投影对齐：`view.(*View).Project` 单次扫存活槽，同一行各列在同一次循环里一起 append。钉于 `TestProjectAlignment`。
4. 失败不留痕：`col.(*Store).locate` 先校验后变更，空列集在 `view.(*View).Project` 入口拒绝，api 遇错直接返回不加锁写。钉于 `TestRejectedOpsLeaveNoTrace`（三类哨兵互不相同另由同函数 errors.Is 断言）。
