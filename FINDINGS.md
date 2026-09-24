# FINDINGS

## 表 1：重写规则在 3^n 穷举下的等价性

| 规则 | 穷举规模 | 逐点等价 | 适用位置 |
| --- | --- | --- | --- |
| const-eval | n=0（无列） | 是 | 任意 |
| and-true / and-false | 3^3=27 | 是 | 任意 |
| or-false / or-true | 3^3=27 | 是 | 任意 |
| not-not | 3^3=27 | 是 | 任意 |
| de-morgan-and | 3^3=27 | 是 | 任意 |
| de-morgan-or | 3^4=81 | 是 | 任意 |
| single-kid | 3^3=27 | 是 | 任意 |
| dup-kid | 3^4=81 | 是 | 任意 |
| contradiction (A AND NOT A → false) | 3^3=27 | 否（仅过滤等价） | 仅顶层合取项 |
| x=x → true | — | 否（连过滤等价也不成立） | 不采用 |
| A OR NOT A → true | — | 否（连过滤等价也不成立） | 不采用 |

测试：`fold.TestRuleEquivalence` 对每条规则全量穷举，失败时打印规则名与
反例赋值；`fold.TestThreeValuedSemantics` 固定 NULL 赋值验证三条关键语义。

## 表 2：200 棵随机树重写统计（固定种子，深度 ≤5，4 列，3^4=81 赋值穷举）

| 指标 | 测试种子 42 | demo 种子 1 |
| --- | --- | --- |
| 等价验证失败数 | 0 | 0 |
| 平均节点数变化 | -0.68 | -0.64 |
| 平均迭代轮数 | 1.74 | 1.74 |
| 轮数上界 4×节点数 违规 | 0 | 0 |

测试：`rules.TestRandomFixpoint`（200 棵逐棵 FilterEqual 并断言计数器上界）、
`rules.TestBoundaries`（空树/单常量/1000 深链/全 NULL/退化/重复子式）、
`rules.TestOscillation`（互逆规则检出震荡）、`rules.TestDeterminism`（打乱 20 次）。

下推：`push.TestPush` 验证单表合取项落到对应 Scan、跨表项留在 Join、未知表与
空谓词为可判定错误；`push.TestCheck` 验证自检能检出人为埋入的跨表引用。
