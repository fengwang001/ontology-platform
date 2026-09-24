# 设计：三值逻辑谓词常量折叠与下推

## 1. 三值逻辑与三条不成立的重写

采用 Kleene 三值逻辑：`T / F / U(未知)`，`NOT U=U`，`AND/OR` 为带 U 的短路真值表：
`F AND x=F`、`T AND x=x`、`U AND T=U`；`T OR x=T`、`F OR x=x`、`U OR F=U`。
比较对 NULL 敏感：任一操作数为 NULL 结果为 U（`=`/`<` 等均同）。

1. `x = x` 不能折叠为真：`x=NULL` 时 `NULL=NULL=U ≠ T`。
2. `A OR NOT A` 不能折叠为真：`A=U` 时 `U OR NOT U = U OR U = U`。
3. `NOT(A AND B) = NOT A OR NOT B` 仍成立：逐项九组赋值（含 U）两边一致，
   例如 `A=B=U`：左 `NOT(U AND U)=NOT U=U`，右 `U OR U=U`。
   但 `A AND NOT A` 不能折叠为假：`A=U` 时 `U AND U=U ≠ F`。

## 2. 何时可按二值优化（安全位置）

WHERE 只放行 T：定义过滤值 `fw(T)=放行`，`fw(F)=fw(U)=拒绝`。
故**在 WHERE 语境下 U 与 F 等价**，但这是语境性质，不是表达式恒等：
`NOT F=T`（放行）而 `NOT U=U`（拒绝），一旦被 NOT 包裹，差异被放大。

结论：设 `p` 为从 WHERE 根到某子表达式的路径上 NOT 节点的个数。
- `p` 为偶数（极性为正，且不在 CASE 判定分支内）：该位置可按二值优化，
  即把常量 U 当 F：顶层独立谓词、顶层 AND 的合取项可折叠/丢弃；
  顶层 OR 中 `U OR Q` 可化为 `Q`（U 不放行，不影响是否放行）。
- `p` 为奇数（NOT/CASE 之内）：禁止任何把 U 与 F 混同的化简，
  `x=x`、`A OR NOT A`、`A AND NOT A` 一律原样保留。

实现方式：结构规则只做三值恒等变换（对 U 也成立）；
`U→F` 的二值收敛仅由一次 seal 收尾完成，并带偶数 NOT 深度参数，
因此同一子表达式在顶层被收敛、在 NOT 下保持原样。

## 3. 规则集（全部为三值恒等，除 seal）

- eval-const：常量比较/常量 NOT/常量 AND/OR 求值；
- flatten：嵌套同名 AND/OR 展平；dedup：按规范化串去重；
- absorb：`T AND P→P`、`F AND P→F`、`F OR P→P`、`T OR P→T`
  （吸收常量，不碰 U；单项 AND/OR 退化为其子节点）；
- demorgan：仅单向 `NOT(AND)→OR(NOT…)`、`NOT(OR)→AND(NOT…)`；
- push-down：合取项若仅引用一张表的列，移入该表 Scan 的过滤。
- seal：偶数 NOT 深度把常量 U 收敛为 F（WHERE 专用，非三值恒等）。

## 4. 不动点终止性与复杂度

每条结构规则严格下降良基度量 `M=(节点数, 规范化串序)`：
展平/吸收/下推减少节点数；dedup 减少子项；demorgan 单向（NOT 下移后叶子
NOT 被 eval-const 处理或保留为原子否定），且不存在反向规则，
故规则对之间不互逆、不震荡；每规则对不动点幂等。
节点数单调不增、每轮至少下降一个度量，故轮数 < 初始节点数 N；
引擎以 `4N` 为硬预算，超限或检出互逆活跃（两规则交替使同一度量回升）即报可判定错误。
每轮一次后序遍历，节点访问次数 ≤ 轮数·N·4（重建系数 4）。

## 5. 下推正确性与自检

Plan 为 `Scan(table, filter) / Filter(plan, pred) / Join(p,q)`。
把顶层（偶数 NOT 深度）合取项按自由列所属表分类：仅属单表者并入该 Scan 的
filter（AND 合并），跨表项留在 Filter。Scan filter 是该表 WHERE 语境，
适用同样的 seal。完成后遍历每个 Scan，断言其 filter 不含其他表列，
出现跨表残留或引用不存在表即返回可判定错误。

## 6. 确定性

AND/OR 子节点一律按规范化串排序，dedup/flatten 结果与输入顺序无关；
规则固定为上述顺序（声明：正规形唯一，系统是合流归约系统，洗牌规则顺序
只影响诊断与预算，不改正规形）。洗牌 20 次规则顺序，断言逐字节一致。

## 7. 等价性验证

`equiv` 对 n 列取域 `{NULL,0,1}`（含 NULL 的三值小整数域），
枚举全部 3^n 赋值，三值逐值比较（另有 WHERE 模式比较 fw）。
每条规则用 n=4（81 组）验证；200 棵固定种子随机树验证不动点前后等价，
失败时报告规则名与具体赋值。深度 1000 的树用迭代遍历，避免栈溢出。
