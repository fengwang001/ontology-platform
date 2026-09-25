# gossip 均值聚合器 NOTES

## 第三节：六步推导（初始 A=0 B=5 C=7，Sum=12，均值 4）

| 步 | 交换 | total | 小半→ | 大半→ | A | B | C | Sum | Spread |
|---|---|---|---|---|---|---|---|---|---|
| 1 | A,B | 5(奇) | A=2 | B=3 | 2 | 3 | 7 | 12 | 5 |
| 2 | B,C | 10(偶) | 平分 5 | — | 2 | 5 | 5 | 12 | 3 |
| 3 | A,C | 7(奇) | A=3 | C=4 | 3 | 5 | 4 | 12 | 2 |
| 4 | A,B | 8(偶) | 平分 4 | — | 4 | 4 | 4 | 12 | 0 |
| 5 | B,C | 8(偶) | 平分 4 | — | 4 | 4 | 4 | 12 | 0 |
| 6 | A,C | 8(偶) | 平分 4 | — | 4 | 4 | 4 | 12 | 0 |

- (甲) 错写「都取 total/2 向下取整」：第 1 步 A=B=2（C=7，Sum=11）。续跑：s2 B,C→4,4；s3 A,C→3,3；s4 A,B→3,3；s5/s6 不变。最终 A=3,B=3,C=3，**Sum 漂移为 9**（12→9，每遇奇 total 丢 1，共丢 3）。
- (乙) 只更新发起方 i：第 1 步后 A=2、B=5、C=7，**Sum=14**。违反不变量 1（求和守恒）。
- (丙) 奇数分配掷硬币：同一六步序列结果**不唯一**（如第 1 步可能得 A=3,B=2，后续全部不同），破坏不变量 3（确定性 / 与朴素参照一致）。

## 第二节：四条不变量 → 代码位置 → 钉住它的测试

1. 求和守恒：`avg.Split` 奇数时 lo+hi=total 精确守恒；`gossip.Exchange` 用返回值整体替换两节点。测试 `TestSumAndSpreadInvariants`、`TestConcurrentExchangeSumConserved`。
2. 离差不增：交换后两值均落在原 [min,max] 区间内，`gossip.Spread` 取 max-min。测试 `TestSumAndSpreadInvariants`。
3. 确定性：`avg.Split` 只由 (id, v) 决定，无随机无外部状态。测试 `TestMatchesNaiveReference`（对照测试内独立手推的 naiveStep）。
4. 失败不留痕：`gossip.Add`/`Exchange` 先完成全部校验再写任何字段。测试 `TestRejections`（含哨兵错误互不相同、状态不变、可继续用）。
