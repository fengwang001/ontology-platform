# FINDINGS：测试结论与可信度对照

## 测试结论（按测试组追加）

- fanout：200 分片、上限 8 时历史峰值恰好为 8（不超过上限）；上限 1 时峰值为 1（完全串行）；
  结果按输入顺序返回。截止 50ms、全部分片挂起时，发起查询数不超过 8（截止后新发起为 0），
  Wait 在截止后极短窗口内返回，200 个结果均为 context.DeadlineExceeded 且保留声明上界。
- combine：五种聚合值正确；TopK 并列按 ID 升序；K 大于总条数时截断为全部；空分片 ID 合法；
  全部空结果（Count=0、HasMinMax=false）与全部失败（ErrAllFailed）可区分；零分片为
  ErrNoShards；重复投递按 ShardID 去重，Count/Sum 与单次投递逐位相同；20 种到达顺序下
  合并结果（含缺失清单）逐字节相同。
- confidence：15 格矩阵（五聚合 × 全成功/缺一/缺一半）全部符合表一；TopK 可信前缀在
  缺失上界之和为 1 时 = K、为 5 时 = 2、为 100 时 = 0；成功分片全空且缺分片时 Min/Max
  为 Uncertain，Count 退化为 ">= 0"。
- report：缺失清单与每分片状态（ok/timeout/corrupt/error）按 ShardID 排序输出，部分失败
  时 Count 标注 ">= 3"、Min 标注 "<= 3"，与到达顺序无关。

## 表一：五种聚合 × 失败情形下的可信度

| 聚合 | 全部成功 | 缺一个分片 | 缺一半分片 |
| --- | --- | --- | --- |
| Count | 精确（exact） | 下界：真实值 ≥ 当前值 | 下界：真实值 ≥ 当前值（更松） |
| Sum | 精确（exact） | 下界：真实值 ≥ 当前值 | 下界：真实值 ≥ 当前值（更松） |
| Min | 精确（exact） | 单边界：真实 Min ≤ 当前值 | 单边界：真实 Min ≤ 当前值（更松） |
| Max | 精确（exact） | 单边界：真实 Max ≥ 当前值 | 单边界：真实 Max ≥ 当前值（更松） |
| TopK | 精确（可信前缀 = K） | 整体不可信；可信前缀 = 值大于缺失上界之和的最长前缀 | 整体不可信；缺失上界之和更大，前缀更短甚至为 0 |

## 表二：四种故障注入的检出方式与分片状态

| 故障注入 | 检出方式 | 报告中的分片状态 |
| --- | --- | --- |
| 超时（永不返回） | ctx 截止，Err 为 context.DeadlineExceeded（errors.Is 可判） | timeout |
| 损坏（Claimed 与实际条数不符） | Claimed != len(Records)，记 ErrCorrupt（errors.Is 可判） | corrupt |
| 重复返回（同一分片到两次） | 按 ShardID 去重，先到为准，不重复计数 | ok（仅计一次） |
| 全部失败 | 成功数为 0，返回哨兵错误 ErrAllFailed（errors.Is 可判） | error/timeout |
