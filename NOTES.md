# NOTES

## 五行表（圆心 / r（r²） / 边界点）
| 点集 | 圆心 | r（r²） | 边界点 |
|---|---|---|---|
| S1={(0,0)} | (0,0) | r=0（r²=0） | (0,0)，1 点 |
| S2={(0,0),(6,0)} | (3,0) | r=3（r²=9） | (0,0)、(6,0)，2 点 |
| S3={(0,0),(6,0),(0,8)} | (3,4) | r=5（r²=25） | 三点（直角，斜边即直径），3 点 |
| S4={(0,0),(6,0),(1,1)} | (3,0) | r=3（r²=9） | (0,0)、(6,0)，2 点 |
| S5={(0,0),(3,0),(6,0)} | (3,0) | r=3（r²=9） | (0,0)、(6,0)，2 点 |

（甲）S2：圆心 (3,0)，r=3。误实现成「圆心=第一点、半径=完整距离」：圆心错成 (0,0)、半径错成 6（r²=36），圆虚大一倍，非最小。
（乙）S4 为钝角三角形（边平方 2+26<36，钝角在 (1,1)）：r=3、r²=9，边界仅 (0,0)、(6,0)。若总取外接圆：圆心错成 (3,-2)，r²=13（r=√13≈3.61），(1,1) 被误放上边界。
（丙）S5 共线：圆心 (3,0)、r=3、r²=9，边界为两端点。若无共线回退硬求外接圆，公式分母 2·Orient=0，除零、构造失败（浮点则 NaN/Inf），根本得不出圆。

## 四条不变量 → 代码保证位置 / 钉住的测试函数
1. 与暴力一致：`api.go` bruteForce+SelfCheck、`mec.go` welzl/trivial；TestMatchesBruteForce、TestSelfCheck。
2. 最小性：`api.go` bruteForce 枚举全部单点/点对直径/三点外接（含回退）候选取 r² 最小者；TestMatchesBruteForce。
3. 覆盖正确、边界恰在圆上：`circ.go` Contains 用 ≤ 比较、From* 设置 B；TestCoverageAndBoundary、TestFivePointSets。
4. 失败不留痕：`api.go` Insert 先做越界/重复校验、全部通过才改状态；TestRejectedOpsLeaveNoTrace、TestSentinelErrors。
另：复杂度计数为 `mec.go` 非导出字段 checks，白盒 TestInteriorInsertionSingleCheck 逐插入断言 ==1（100..10000 档）；demo 只能拿到 bool，无数值出口。
并发：`api.go` sync.RWMutex 保护，TestConcurrentReaders 在 -race 下逐字段比对。
