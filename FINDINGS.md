# FINDINGS

## 表一：薪资更正场景（有效区间 2020 年，t1 写入 100，t2 更正为 120）

| 有效时刻 \ 事务时刻 | t1（更正前） | t2（更正后） |
| --- | --- | --- |
| 2020 | 100 | 120 |
| 2021（有效区间外） | 不存在 | 不存在 |

同一有效时刻 2020 因事务时刻不同得到不同答案，证明事务时间轴未被篡改（`asof.TestSalaryCorrection`）。

## 表二：复杂度实测（1000 键 × 每键 10 版本 = 10000 条）

| 指标 | 实测 | 上界 |
| --- | --- | --- |
| 单次点查检查记录数 | 10（`asof.TestCheckedBound`） | 4 × 10 = 40 |
| 单次更正触及记录数 | 12（10 版本键再更正一次，`store.TestCorrectTouchedBound`） | 10 + 2 = 12 |

## 自检与并发

- 随机 1000 次写入/更正/删除后，`checkDisjoint` 通过（`store.TestRandomOpsKeepDisjoint`）。
- 100 协程 × 100 次并发更正同一键后，`checkDisjoint` 通过且 `-race` 干净（`store.TestConcurrentCorrectKeepsDisjoint`）。
