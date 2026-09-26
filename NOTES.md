# TAC 生成器 NOTES

## 三、(a&&b)||c 九行分步表（指令 | 用到的编号 | 当前结果临时）
1. `t1 = a`            | 新临时 t1        | t1
2. `if t1 == 0 goto L1`| 新标签 L1        | t1
3. `t2 = b`            | 新临时 t2        | t2
4. `t1 = t2`           | 复用 t1（&&汇合）| t1
5. `L1:`               | 落标签 L1        | t1
6. `if t1 != 0 goto L2`| 新标签 L2        | t1
7. `t3 = c`            | 新临时 t3        | t3
8. `t1 = t3`           | 复用 t1（||汇合）| t1
9. `L2:`               | 落标签 L2        | t1（最终结果）

(甲) 短路：正确实现按固定模板，右侧代码排在条件跳转之后、L1 之前——`t1=false; if t1==0 goto L1; t2=1; t3=0; t4=t2/t3; t5=0; t6=t4>t5; t1=t6; L1:`。除法指令静态存在但落在被跳过的区间里，朴素解释器沿 goto 绕过它，1/0 运行时不求值、不崩溃，结果得 false。若不短路：①翻成一条 `t = a && b`——`&&` 不在合法二元操作码表内，解释器无法执行（整体失败）；②先翻译右侧再判左值——`t4=t2/t3` 在左值判断之前执行，a 为假也照样除零 panic，得不出 false。
(乙) 左结合 `a-b-c`：`t1 = a; t2 = b; t3 = t1 - t2; t4 = c; t5 = t3 - t4`，a=10,b=3,c=2 得 5。若错按右结合：先算 `t3 = t2 - t4`（b-c）再 `t5 = t1 - t3`，得 10-(3-2)=9，错。
(丙) `(if c then 1 else 2)+3` 正确：then/else 都拷贝进同一汇合临时 t3，`t6 = t3 + t5` 两路都读 t3。若不汇合（then 结果留 t2、else 留 t4，`+3` 固定读 t2）：c 为假时走 else，t2 从未赋值，`+3` 读到未初始化值，应得 5 却得到错误结果。

## 二、四条不变量：保证位置 | 钉住它的测试
1. 与朴素参照一致：tac.gen 逐节点翻译规则 + api.SelfCheck 内置逐指令解释器与 AST 递归求值（含短路/左结合）比对 | TestSemanticsMatch、TestSelfCheck
2. 短路不触发不可达求值：tac.genBinary 中 &&/|| 先译左侧、发条件跳转，右侧代码只在跳转之后生成，短路路径被 goto 绕过；SelfCheck 用遇除零即 panic 的解释器验证不触发 | TestShortCircuitNoDeadCode
3. 临时连续、结果落在最后临时且不被覆盖：tac.generator.tmp 单调计数、结果临时只在汇合拷贝中被重写 | TestTempNumbering、TestResultTemp
4. 失败不留痕：tac.Gen 用局部 generator 整体生成，任一分支报错即返回 nil、不回传半成品 | TestRejectedInputs
