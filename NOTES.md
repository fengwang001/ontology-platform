# NOTES — 变更流压实（stream compaction）

## 一、第三节推导

记多重集为 `{v:份数}`，初始 I=∅，键 g 上流：Insert7 Insert7 Retract7 Retract7 Insert7 Insert3。

| 步 | 事件 | 施加后 g 的多重集 |
|---|---|---|
| 1 | Insert 7 | {7:1} |
| 2 | Insert 7 | {7:2} |
| 3 | Retract 7 | {7:1} |
| 4 | Retract 7 | {} |
| 5 | Insert 7 | {7:1} |
| 6 | Insert 3 | {7:1, 3:1} |

**(甲) 可消对（逐对指认）**：先消相邻的 **(2,3)**＝Insert7 紧跟 Retract7——先插后撤对任意状态都是恒等，总能消；(2,3) 删掉后 **(1,4)** 在存活序列中相邻，同型再消。两对消尽，终态不变。**(4,5)**＝Retract7 紧跟 Insert7，终态虽也等价，但 `Insert∘Retract` 是**偏函数**：Retract 只在该点 7 的份数 ≥1 时有定义。前提：该 Retract 的合法性由 I 或保留下来的更早事件担保。反例 I=`{g:{}}`（7 一份都没有），S=`Retract7, Insert7`：S 在 I 上第一步就撤回不存在的值，非法；若压实器无 I 意识、见互逆就消成空流，空流在 I 上合法，非法被“洗白”——这正是不变量 3 反向不做保证、必须由 Replay 独立判 ErrIllegalRetract 的原因。故本实现栈中只允许 Retract 抵消栈顶 Insert，绝不反向消。

**(乙)** 合法压实结果：`[Insert7(步5), Insert3(步6)]`。第 1 步后两边都是 {7:1}，**第 2 步起首次偏离**：原流第 2 步后是 {7:2}，压实流第 2 步后已到终态 {7:1,3:1}（{7:2}、{7:1}、{} 三个中间状态被跳过）。若要求逐步相同，则任何事件都不能删：Insert/Retract 都严格改变多重集（份数 ±1），删掉任一条就少一个必经状态；缩短即偏离，长度必须相等且事件逐条相同，故 Compact 只能是恒等函数。

## 二、四条不变量：保证位置与钉测

1. **终态等价**：fold/fold.go 每次消的都是同键同值的一插一撤（净零），重排不改变各 (键,值) 净份数。钉测：`TestCompact_TerminalEquivalence`，api 侧 `TestSelfCheck`。
2. **不增长且幂等**：fold/fold.go 只删不增；存活序列重放时每步栈顶判定与首次完全一致，故再压一次结果不变。钉测：`TestCompact_NoGrowthIdempotent`。
3. **合法性保持**：被消 Insert 与抵消它的 Retract 之间不存在存活的同 (键,值) 事件（LIFO），其余事件的前缀计数与原流逐点相同，Retract 永不会因压实而缺钱。钉测：`TestCompact_LegalityPreservation`（api 侧 `TestSelfCheck` 同样核验）。
4. **失败不留痕**：fold.go 先逐条校验再消解；api.go 先查长度与事件合法性、再在全新副本上建状态，任何失败都返回 nil + 哨兵错误。钉测：`TestCompact_InvalidAtomic`、`TestAPI_Errors`（三类哨兵互不相同）、`TestAPI_RejectedStillUsable`。
