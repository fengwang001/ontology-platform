# 结论记录

## 统计异常的检出与标注

| 异常 | 检出方式 | explain 标注 |
| --- | --- | --- |
| 缺失 | 谓词列在 `stats.Table.Columns` 中查不到，选择率回退 `DefaultSelectivity=0.1`，节点置 `Unreliable` | `[unreliable estimate]` |
| 过期 | `|statsRows-catalogRows|/catalogRows > 0.1`，按目录行数校正，节点置 `Stale` | `[stale stats: catalog rows N used]` |
| 损坏 | `Histogram.Validate`：桶计数和 ≠ 总行数，或桶边界非严格递增；`catalog.AddTable` 返回包装 `ErrCorruptStats` 的错误并含表名/列名 | 计划不可生成，错误文本 `corrupt statistics: table T column C: ...` |

三者分别包装 `ErrMissingStats` / `ErrStaleStats` / `ErrCorruptStats`，`errors.Is` 可区分；
`catalog.Check()` 把缺失与过期作为诊断错误返回，损坏在登记时即拒绝。

## n=4 链式谓词各子集最优计划

场景：链 A-B-C-D，行数 A=1000、B=2000、C=3000、D=4000，各连接列 NDV=100
（选择率 0.01）。下表为 `plan.Optimize` 实测输出（计划以中序叶子表名序列表示）：

| 子集 | 最优计划 | 估计基数 | 代价 | 备注 |
| --- | --- | --- | --- | --- |
| {A} | A | 1000 | 1000 | 扫描 |
| {B} | B | 2000 | 2000 | 扫描 |
| {C} | C | 3000 | 3000 | 扫描 |
| {D} | D | 4000 | 4000 | 扫描 |
| {A,B} | A⋈B | 2e4 | 2.003e6 | |
| {A,C} | A×C | 3e6 | 3.004e6 | 不连通，只能笛卡尔积 |
| {A,D} | A×D | 4e6 | 4.005e6 | 不连通，只能笛卡尔积 |
| {B,C} | B⋈C | 6e4 | 6.005e6 | |
| {B,D} | B×D | 8e6 | 8.006e6 | 不连通，只能笛卡尔积 |
| {C,D} | C⋈D | 1.2e5 | 1.2007e7 | |
| {A,B,C} | (A⋈B)⋈C | 6e5 | 6.2006e7 | |
| {A,B,D} | (A⋈B)×D | 8e7 | 8.2007e7 | 分量 {A,B}、{D}，笛卡尔积在顶端 |
| {A,C,D} | A×(C⋈D) | 1.2e8 | 1.32008e8 | 分量 {A}、{C,D}，笛卡尔积在顶端 |
| {B,C,D} | (B⋈C)⋈D | 2.4e6 | 2.46009e8 | |
| {A,B,C,D} | ((A⋈B)⋈C)⋈D | 2.4e7 | 2.41401e9 | 连通子集的计划绝不出现笛卡尔积 |

观察：不连通子集（{A,C}、{A,D}、{B,D}、{A,B,D}、{A,C,D}）的最优计划含笛卡尔积
且笛卡尔积位于计划树顶端；所有连通子集的计划均无笛卡尔积，验证了推迟规则。
