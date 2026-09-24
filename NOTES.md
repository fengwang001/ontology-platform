# 变更流压实推导

## 一、六步流（I=∅，仅列分组 g；v^n 表示值 v 活着 n 份）

| 步 | 事件 | 施加后多重集 |
|---|---|---|
| 1 | Insert 7 | {7^1} |
| 2 | Insert 7 | {7^2} |
| 3 | Retract 7 | {7^1} |
| 4 | Retract 7 | {} |
| 5 | Insert 7 | {7^1} |
| 6 | Insert 3 | {7^1, 3^1} |

(甲) 可消对：**(2,3)**（相邻 I7→R7）；消去后原步 1、4 相邻成 I7→R7，**(1,4)** 亦可消；(4,5) 为 R→I 不许消，其余同值同向或异值。
`Insert v→Retract v` 恒可消：计数 c→c+1→c，净效应 0，撤回必合法。
`Retract v→Insert v` 仅当该 R 由流内更靠前的**存活 Insert** 担保时才可消（而那种 R 按 I→R 规则本已被消）；只能由初始状态担保的 R 不能消。
反例取 I=∅（v 份数 0）、S=[R v, I v]：若消成 []，原流在该 I 上第一步即整体报"撤回不存在"，[] 却合法返回终态 ∅——同一 I 上一边失败一边出终态，结果不再"完全相同"，不变量 3 的合法性对应失败；不知道 I 时 R→I 一律不消。
(乙) 合法压实结果 [Insert 7, Insert 3]；按事件序号对齐，**第 2 步**首次偏离（原流 {7^2}，压实流 {7^1}），终态相同。
逐步相同⇒恒等：任一事件都使某计数 ±1，由相邻状态 s→s′ 可唯一反推该事件；逐步相同迫使压实流第 k 个事件恰为原流第 k 个，长度又不许增，故任何真正缩短都不可能。

## 二、四条不变量：保证位置 / 钉住的测试

1. 终态等价：fold.go 只消逆对，每对净效应 0；测试 `TestInvariants`、`TestSelfCheck`。
2. 不增长+幂等：fold.go 只标记存活事件，输出中再无可消对；测试 `TestInvariants`、`TestSelfCheck`。
3. 合法性保持：fold.go 只消 I→R（该撤回被流内同一份 Insert 担保，对任意 I 合法）；测试 `TestInvariants`。
4. 失败不留痕：fold.go 先整体校验再消解，非法返回 nil；api.go Replay 先深拷贝 init、出错只返回 err；测试 `TestSentinels`、`TestReusable`。
比较次数：fold.go 非导出字段 `comparisons`，每事件至多与栈顶比 1 次（atomic，允许并发污染），对外只回布尔；测试 `TestLinear`；并发等价测试 `TestConcurrent`。
