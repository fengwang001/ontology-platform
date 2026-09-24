# 计数布隆过滤器 NOTES

## 第三节推导（m=7, k=3, maxCount=2）

双重散列位置：x=1 → {1,3,5}；x=4 → {4,2,0}；x=3 → {3,0,4}；x=2 → {2,5,1}。

| 步 | 操作 | 计数器 [0..6] | 结果 |
|---|---|---|---|
| 1 | Insert(1) | [0,1,0,1,0,1,0] | 成功 |
| 2 | Insert(4) | [1,1,1,1,1,1,0] | 成功 |
| 3 | Insert(3) | [2,1,1,2,2,1,0] | 成功 |
| 4 | Insert(3) | [2,1,1,2,2,1,0] | 失败 ErrOverflow（位置 0,3,4 已达 2），状态不变 |
| 5 | Contains(2) | [2,1,1,2,2,1,0] | true（假阳性：2 未插入，但 2,5,1 全 >0） |
| 6 | Delete(2) | [2,1,1,2,2,1,0] | 失败 ErrNotInserted，状态不变 |
| 7 | Contains(1) | [2,1,1,2,2,1,0] | true |

(甲) 第 4 步：位置 0,3,4 当前均已等于 maxCount=2，正确实现整条 Insert 拒绝、不加一，[0]=[3]=[4]=2 保持；不查 maxCount 的朴素实现会把它们错加成 3,3,3（正确应仍为 2）。
(乙) 第 6 步：2 从未插入，正确实现返回 ErrNotInserted，计数器不变。若朴素实现只查「k 个位置全 >0」就放行删除，会减位置 2,5,1：[1] 与 [5] 错成 0，随后第 7 步 Contains(1) 因位置 1,5 为 0 错成 false（正确应为 true）。
(丙) 第 3 步后计数器之和 = 9 = k×插入次数 = 3×3。若朴素实现把溢出 Insert 静默饱和在 maxCount（不加一、不报错、照算成功），第 4 步后之和仍为 9，与「4 次成功插入 × k = 12」差 3。

## 四条不变量：保证位置与钉住测试

1. 无假阴性：Delete 只减「keys 精确计数 >0」的键的计数器，插入过的键计数器必 >0（cbf/cbf.go Delete 先查 keys）；测试 TestNoFalseNegatives。
2. 计数守恒：Insert/Delete 仅在成功时把 k 个计数器 ±1 并同步 keys 计数，被拒绝的操作一行修改都不执行（cbf/cbf.go Insert/Delete 先验后改）；测试 TestCounterConservation。
3. 与朴素重算一致：Contains 只读 hash.Positions 给出的 k 个位置判全 >0，无其他状态参与（cbf/cbf.go Contains）；测试 TestMatchesNaiveModel。
4. 失败不留痕：New/Insert/Delete 全部先完成校验再写状态，任何拒绝路径在第一个写操作之前 return（hash.Validate、cbf.Insert 溢出预检、cbf.Delete 存在性预检）；测试 TestRejectedOpsLeaveStateUnchanged。
