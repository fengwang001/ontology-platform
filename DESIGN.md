# 常量折叠与谓词下推重写器 — 设计

## 1. 三值逻辑（Kleene）真值表

真值：`T` 真、`F` 假、`U` 未知（NULL 比较结果）。
- AND = min（F<U<T 危险序）；OR = max；NOT 交换 T/F，`NOT U = U`。
- 比较：任一操作数为 NULL ⇒ U。`x = x` 当 x=NULL 时为 U。

## 2. 三条常见重写为何不成立

1. `x = x → T`：x=NULL 时左=U，右=T，不等价。任何位置都不折叠。
2. `A OR NOT A → T`：A=U 时 `U OR NOT U = U OR U = U ≠ T`。任何位置都不折叠。
3. `A AND NOT A → F`：A=U 时 `U AND U = U ≠ F`。
   德摩根 `NOT (A AND B) = NOT A OR NOT B`：对 9 组取值逐一代入真值表均相等，三值下成立，可无条件展开。

## 3. 何时可按二值（WHERE 语义）优化

WHERE 过滤只保留 T，因此 `F` 与 `U` 过滤效果相同。定义谓词级等价
`p ≃w q`：对所有赋值，`p=q=T` 或两者都 ∈{F,U}。
- `NOT p`、`CASE` 条件分支会把 U 与 F 区分开：`NOT F=T` 而 `NOT U=U`。
- `OR` 的一支若为 U，另一支为 T 仍得 T，U 可被"救活"，分支内也不安全。
- `AND` 中 U 与 F 对结果是否保留的影响一致。

故**安全位置（safe）**定义（自顶向下）：根为 safe；AND 的每个直接子项继承
safe；进入 NOT、OR、CASE 子树即变 unsafe。仅在 safe 位置允许
`A AND NOT A → F`（WHERE 语义等价：左永不=T，右=F）。`x=x`、
`A OR NOT A` 即使在 safe 位置也不折叠（NULL 行可能被其他合取项救活时结果仍不同：
`x=x` 本身为 U 永不为 T，但其作为合取项被折叠成 T 会改变该行是否被保留——
实际 `x=x` 所在行永不过滤，折成 T 后该行可能通过其余条件，语义改变）。

测试让同一 `A AND NOT A` 分别处于顶层（折叠为 F）与 NOT 之下（保持原样）。

## 4. 不动点终止性

规则集（rules 包内固定顺序）：
1 CmpConsts（两常量比较求值）；2 NullCmp（NULL 参与比较→U）；
3 AndConst（合去 T、吸收 F/U-in-unsafe 以外常量，safe 位 U≡F）；
4 OrConst（吸收 T、合去 F）；5 NotNot；6 NotConst；7 NotCmp 翻转比较符；
8 DeMorgan（仅展开）；9 XorNeqElim 等无；10 SelfContradiction（仅 safe：
`A AND NOT A → F`）；11 UnaryBool 退化（单子 AND/OR→子节点）；12 DupElim。

测度（字典序）：节点总数 → 否定深度 → 规范化串。除 DeMorgan、NotCmp 外每
条规则严格减小节点数；DeMorgan 把 NOT(AND/OR) 变成 AND/OR，否定深度下降且有
NotNot/NotCmp 配合，规范化串在固定方向上单调；NotCmp 消除 NOT。因此规则集
无互逆对，有限树上测度良序，迭代必然终止。引擎对每轮结果做指纹，若指纹重复
（即出现互逆规则导致震荡）立即返回可判定错误 `ErrOscillation`。硬上界
`4*节点数` 轮；节点访问次数 ≤ `轮数*节点数*4`（每轮自顶向下标 safe 一趟 +
自底向上变换一趟，各 ≤2N）。

## 5. 谓词下推

计划节点：Scan(表)、Join(左右)、Filter(输入, 谓词)。Filter 的谓词取顶层合取
范式（拆 AND 链），每个合取项求其引用表集合：单表则挂到该表 Scan 上（与
Scan 原谓词 AND）；多表或含不存在表（后者报 ErrUnknownColumn）留在 Filter。
Join 自底向上处理后，Filter 若全部下推则退化为其子输入。完成后自检
CheckScopes：每个 Scan 谓词只引用本表。

## 6. 等价验证器

n 列、域 {NULL,0,1}，混合进制枚举 3^n 赋值。eval 实现 Kleene 求值；
`Equiv` 比较三值，`EquivWhere` 比较"是否=T"。随机树生成器固定种子，
供规则单测与 200 棵不动点等价测试使用，失败时报告规则名与赋值向量。

## 7. 边界

nil 树→ErrEmpty；深度 1000 链用显式栈迭代，不递归；全 NULL 常量折叠为 U；
单子 AND/OR 退化；重复子表达式不去 CSE 但不报错；确定性来自固定规则顺序与
规范化打印，打乱规则切片顺序不改变结果（顺序敏感规则已在第 4 节固定）。
