# TAC 生成器：推导与不变量

## 三、`(a&&b)||c` 分步表（内层 && 先算，其结果临时 t1 被外层 || 直接复用）

```
 #  指令/标签              新编号   当前结果临时
 1  t1 = a                 t1      t1
 2  if t1 == 0 goto L1     L1      t1
 3  t2 = b                 t2      t2
 4  t1 = t2                —       t1
 5  L1:                    —       t1
 6  if t1 != 0 goto L2     L2      t1
 7  t3 = c                 t3      t3
 8  t1 = t3                —       t1
 9  L2:                    —       t1（最终结果）
```

- (甲) 会出现：`false&&(1/0>0)` 的序列静态含 `t2=1; t3=0; t4=t2/t3; …`，但首条 `if t1==0 goto L1` 使其运行时不可达，朴素解释器一条也不执行，无除零，结果 t1=0(false)。不做短路则：把 `&&` 当普通二元 `t=a&&b` 需先求出两侧，或先翻译右侧——`t4=t2/t3` 都落在主路径上被真正执行，解释器除零报错，整个求值失败。
- (乙) 正确：`t1 = a - b`、`t2 = t1 - c`，即 (a-b)-c。若误作右结合：先算 `t1 = b - c` 再 `t2 = a - t1`，得 a-(b-c)；a=10,b=3,c=2 时正确值 5，错误值 9。
- (丙) 正确：then/else 各自求值后都拷贝进同一汇合临时 t3，`+3` 读 t3。若不汇合（then→t2、else→t4，`+3` 固定读 t2）：c 为假时 else 只写 t4，t2 从未被写，`+3` 读到未初始化的 t2(=0)，得 0+3=3 而非 2+3=5；固定读 t4 则 c 为真时错成 3 而非 4。

## 二、四条不变量：保证位置 / 钉住测试

1. 与朴素参照一致：`tac.gen.expr` 严格左到右、短路展开；`api.SelfCheck` 对内置 AST 集比对 `tac.Exec` 与参照求值。测试 `TestMatchesReference`。
2. 短路右侧运行时不求值：`tac/gen.go` 的 `logic` 把右侧指令放在条件跳与标签之间，执行时一条不跑。测试 `TestShortCircuitSkipsRight`。
3. 临时连续、结果唯一：`tac.gen.tmp` 单调递增分配，结果即最后产生的临时且之后不被覆盖。测试 `TestTempsSequentialResultLast`。
4. 失败不留痕：`tac.Gen` 每次用全新 `gen`，出错返回 `nil` 与三类互异哨兵错误，后续调用不受影响。测试 `TestRejectionLeavesNoTrace`、`TestErrorsDistinct`。
