# 结论

## 表 1：重写规则的穷举验证（equiv，n=3 共 27 种赋值）

| 规则 | 3^n 穷举是否等价 | 适用位置限制 |
|---|---|---|
| const-eval（NOT/EQ/AND/OR 全常量求值） | 等价 | 任意位置 |
| and-annihilator（AND 含 FALSE → FALSE） | 等价 | 任意位置 |
| or-annihilator（OR 含 TRUE → TRUE） | 等价 | 任意位置 |
| and-identity（AND 去 TRUE） | 等价 | 任意位置 |
| or-identity（OR 去 FALSE） | 等价 | 任意位置 |
| and-not-a（A AND NOT A → FALSE） | 3VL 下不等价（A=U 时为 U），过滤语义下等价 | 仅顶层过滤位置（路径不经 NOT/EQ） |
| double-negation（NOT NOT A → A） | 等价 | 任意位置 |
| de-morgan-and（NOT(A AND B) → NOT A OR NOT B） | 等价 | 任意位置 |
| de-morgan-or（NOT(A OR B) → NOT A AND NOT B） | 等价 | 任意位置 |
| x=x → TRUE | 不等价（x=U 时 U≠T），已禁用 | 无（不实现） |
| A OR NOT A → TRUE | 不等价（A=U 时 U≠T），已禁用 | 无（不实现） |

差异定位验证：对错误规则 x=x→TRUE，equiv.Diff 报告的第一组反例赋值
精确为 t.x=U；A AND NOT A→FALSE 的原始 3VL 反例同样精确出现在 t.a=U。

## 表 2：200 棵随机树重写统计

固定种子 42，3 列（t.a/t.b/t.c），生成深度上限 5，指标取自
`go test -v ./rules/ -run TestRandomRewriteEquiv` 的实测输出：

| 指标 | 数值 |
|---|---|
| 平均节点数（重写前） | 12.8 |
| 平均节点数（重写后） | 8.8 |
| 平均迭代轮数 | 1.86 |
| 等价验证失败数 | 0 |

其他实测结论：

- 轮数上界：全部 200 棵均满足 轮数 ≤ 4×节点数；访问量 ≤ 轮数×节点数×4。
- 震荡检测：默认规则集加入互逆规则 neg-intro 后，Rewrite 报
  rules.ErrOscillation（可判定错误）。
- 确定性：打乱规则顺序 20 次，规范化打印逐字节一致。
- 边界：空树报 ast.ErrEmpty；深度 1000 的 NOT 链重写为单列引用，无栈溢出；
  重复子表达式、全 NULL 常量、退化单子节点 AND/OR 均正常。
- 下推：单表合取项进入对应 Scan，连接谓词与常量留顶层；Check 自检
  无跨表引用残留；下推前后整体谓词经 equiv 验证等价；引用不存在的表
  报 push.ErrUnknownTable。
