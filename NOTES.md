# 优先级继承锁 NOTES

## 八步推导（优先级越大越重要）

| # | 操作 | 持有者 | 有效优先级 | 等待队列(FIFO) |
|---|------|--------|-----------|----------------|
| 1 | Acquire(A,1) | A | 1 | （空） |
| 2 | Acquire(B,2) | A | 2 | B(2) |
| 3 | Acquire(C,9) | A | 9 | B(2) C(9) |
| 4 | Release(A)   | C | 9 | B(2) |
| 5 | Acquire(D,5) | C | 9 | B(2) D(5) |
| 6 | Release(C)   | D | 5 | B(2) |
| 7 | Release(D)   | B | 2 | （空） |
| 8 | Release(B)   | （空闲） | — | （空） |

- (甲) 第 3 步后 A 的有效优先级 = max(1, 2, 9) = **9**；若只继承队首 B 的优先级，会错成 **2**。
- (乙) 第 4 步正确新持有者是等待者中静态最高的 **C(9)**；若按 FIFO 队首交接，会错交给 **B(2)**。
- (丙) 若把继承来的 9 永久写回覆盖 A 的静态优先级，A 再次 Acquire 时静态会被记成 **9**（正确应为 **1**）。

## 四条不变量的落点

1. 与朴素参照一致：`pip.Core.Effective` 用最大堆堆顶 O(1) 取等待者最大静态、再与持有者静态取大，等价于朴素扫描；测试 `TestNaiveConsistency` 钉住。
2. 继承正确：`pip.Core.Acquire` 在等待者入堆后立即经 `locateMax` 重算有效优先级；测试 `TestInheritance` 钉住。
3. 交接正确：`pip.Core.Release` 从最大堆弹顶（静态最高等待者）作新持有者；测试 `TestHandoff` 钉住。
4. 失败不留痕：`lock.Lock.Acquire/Release` 先校验（三个互异哨兵错误）后改状态，被拒时不触碰 `pip.Core`；测试 `TestRejectLeavesNoTrace` 钉住。
