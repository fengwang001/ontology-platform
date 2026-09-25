# NOTES — 增量流水线谓词下推

## 第三节推导：链 F1 → S → F2，六事件分步表

| Seq | Val/Kind | F1(偶) | S.max 前→后 | S(新高) | F2(Kind=A) | 输出 |
|---|---|---|---|---|---|---|
| 1 | 10/A | ✓ | -∞→10 | ✓ | ✓ | (1,10,A) |
| 2 | 25/A | ✗(奇) | 10→10 | — | — | 无 |
| 3 | 18/A | ✓ | 10→18 | ✓ | ✓ | (3,18,A) |
| 4 | 30/B | ✓ | 18→30 | ✓ | ✗(B) | 无 |
| 5 | 22/A | ✓ | 30→30 | ✗(22<30) | — | 无 |
| 6 | 40/A | ✓ | 30→40 | ✓ | ✓ | (6,40,A) |

正确输出：Seq1(Val=10)、Seq3(Val=18)、Seq6(Val=40)。

- **(甲)** 错链 `F1→F2→S`：Kind=B 的 Val=30 被 F2 挡住、进不了 S，max 停在 18；于是 Val=22>18 通过 S。输出 = 10,18,22,40，**多出 Val=22（Seq5）**。
- **(乙)** 错链 `S→F1→F2`：Val=25 先到 S 把 max 抬到 25（25 自己随后被 F1 滤掉，但 max 已更新），Val=18<25 被 S 丢弃；同理 Val=22<30 被丢。输出 = 10,40，**丢失 Val=18 与 Val=22**；Val=18 是被 **Val=25** 顶掉的（25 先更新 max=25，18 不再新高）。
- **(丙)** 错链 `p→S`（p=偶且Kind=A）：第 4 步 Val=30/Kind=B 被 p 整体挡在 S 外，S 没见过 30，**第 4 步后 S.max=18**；正确顺序下 S 见过 30，**应为 30**。差值 30-18 的来源：被合并谓词提前滤掉的事件不再参与状态更新。

## 四条不变量的保证位置与钉住测试

1. **与朴素链一致**：`pipe.Plan` 只在段内合并、不动屏障两侧；`api.SelfCheck` 不变量 1 与 `TestNaiveEqualsPlanned`（api/api_test.go）钉住。
2. **无状态可交换**：`pipe.Plan` 段内单遍合并为一个短路 AND；`TestStatelessCommute`（api/api_test.go）钉住。
3. **屏障不可穿越**：`pipe.ValidateMove` 对跨状态过滤器的移动返回 `ErrBarrier`，状态过滤器相对顺序由 Plan 原样保留；`TestBarrierRejected`（api/api_test.go）钉住。
4. **失败不留痕**：`pipe.Pipeline.Feed` 先整批校验事件、全部合法才逐条求值；非法谓词/移动在构建期拒绝、不触碰任何实例；`TestFailureLeavesNoTrace`（api/api_test.go）钉住。
