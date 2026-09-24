# 变更流压实推导

## 第三节推导

六步分步表（I=∅，分组 g，流为 I7 I7 R7 R7 I7 I3）：

| 步 | 事件 | 施加后 g 的多重集 |
|---|---|---|
| 1 | Insert 7 | {7:1} |
| 2 | Insert 7 | {7:2} |
| 3 | Retract 7 | {7:1} |
| 4 | Retract 7 | {} |
| 5 | Insert 7 | {7:1} |
| 6 | Insert 3 | {7:1,3:1} |

(甲) 可消对：(2,3) 与 (1,4)——消去 (2,3) 后 (1,4) 相邻，两对都是 Insert 紧跟 Retract。
Insert 紧跟 Retract（[I v,R v]）无条件可消：净效应为零，且对间状态只比周围多一份，
删去后其余前缀状态逐一不变，合法性保持。
Retract 紧跟 Insert（[R v,I v]）仅当 R 有依据（S 在 I 上合法）才可消。不能消的情形：
I=∅、S=[R7,I7]，S 第 1 步即非法；若消成 []，非法流被洗成合法流，不变量 3 的前提
（可判定的合法性）被架空、第三类错误被掩盖，故压实器对 [R v,I v] 一律不消。

(乙) 一个合法压实结果：[Insert 7, Insert 3]（原第 5、6 步）；第 2 步起中间状态即偏离
（{7:1,3:1} vs 原流 {7:2}）。若要求逐步相同：逐步状态序列相等蕴含长度相等；而每步
事件由前后多重集之差唯一确定（组、值、方向皆可读出），故事件序列相同，Compact 只能
是恒等函数，任何真缩短都不可能。

## 四条不变量的落实位置与钉住测试

1. 终态等价：fold.Compact 只删净零相邻对（fold.go 栈式消解）；TestTerminalEquiv。
2. 不增长+幂等：fold.Compact 只删不增，输出不再含 [I,R] 相邻对；TestShrinkIdempotent。
3. 合法性保持：删 [I,R] 对不改变其余前缀状态（见上）；TestLegality。
4. 失败不留痕：fold.Compact 先校验后压实、出错返回 nil；api.Replay 出错返回 nil；TestErrors。
自检入口 api.SelfCheck 对内置流核验四条，由 TestSelfCheck 钉住。
