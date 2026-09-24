# 三维 CUBE 增量维护：推导与不变量

## 五行分步表（T=(*,*,*)  X=(a,*,*)  Y=(*,b,c)  Z=(a,b,c)）
| 步 | 操作 | T | X | Y | Z |
|---|---|---|---|---|---|
| 1 | Add(a,b,c,+2) | 2 | 2 | 2 | 2 |
| 2 | Add(a,b,c,+3) | 5 | 5 | 5 | 5 |
| 3 | Add(a,d,c,+5) | 10 | 10 | 5 | 5 |
| 4 | Add(e,b,c,+7) | 17 | 10 | 12 | 5 |
| 5 | Remove(a,b,c,+2) | 15 | 8 | 10 | 3 |

- **甲**：(*,*,c)=15（四条事实 C 全为 c：3+5+7=15），(a,*,c)=8（3+5）。ROLLUP(A,B,C) 只有 总计/A/A+B/A+B+C 四层，(*,*,c) 与 (a,*,c) 不在前缀链上：**不存在**，查询只能报缺失（被当成 0）。
- **乙**：再 Add("",b,c,+4)，正确：(*,*,*)=19、(*,b,c)=14，新增 level3 cell ("",b,c)=4。若用 "" 当 ALL 哨兵：A 具体与 A=ALL 撞键，8 掩码塌成 4 键、每键 +8，(*,*,*)=23、(*,b,c)=18。
- **丙**：第5步后非空 cell = 1(level0)+5(l1)+7(l2)+3(l3)=**16**。笛卡尔积物化：(nA+1)(nB+1)(nC+1)=3·3·2=**18** 候选，空 cell **2** 个（(e,d,*)、(e,d,c)）。一般公式 **(nA+1)(nB+1)(nC+1)**，随各维取值数乘积（最坏立方级）膨胀；8-掩码法非空 cell ≤ 8F，只与事实数有关，与维度基数无关。

## 第二节四条不变量：保证位置 / 钉住测试
1. 与批量重算一致：`cube/cube.go` 的 `Add`/`Remove` 对 `dim.Cells` 返回的 8 键同加 ±V，`View` 只留非零 —— **TestBatchEquivalence**。
2. Add/Remove 精确回退：同 8 键、反号、归零即 `delete`（cube.go `Add`/`Remove`）—— **TestAddRemoveRoundTrip**。
3. 层级守恒 1/3/3/1：`dim/dim.go` 的 `Cells` 按 0..7 掩码枚举、`Key.Level` 数非 ALL 维 —— **TestLevelDistribution**。
4. 失败不留痕：超限/缺失均为先预检后写（cube.go `Add` 的 final 计数预检、`Remove` 的八键存在预检）—— **TestRejectionsLeaveState**。
