# 全序广播定序器 NOTES

## 一、十行分步表（d=deliveredUpTo，n=nextSeq，集合=已分配未投递）

| # | 操作 | d | n | 已分配未投递 |
|---|------|---|---|--------------|
| 1 | Propose("A") | 0 | 2 | {1} |
| 2 | Propose("B") | 0 | 3 | {1,2} |
| 3 | Deliver()→A | 1 | 3 | {2} |
| 4 | Propose("C") | 1 | 4 | {2,3} |
| 5 | Deliver()→B | 2 | 4 | {3} |
| 6 | Propose("D") | 2 | 5 | {3,4} |
| 7 | Propose("E") | 2 | 6 | {3,4,5} |
| 8 | Crash() | 2 | 6 | {3,4,5}（空洞） |
| 9 | RePropose(3,C)(4,D)(5,E) | 2 | 6 | {3,4,5}（已填回） |
| 10 | Deliver×3→C,D,E | 5 | 6 | {} |

## 二、三问

- (甲) 崩溃后 d=2、n=6，空洞为 seq {3,4,5}；补发必须沿用原 seq 3、4、5。若 RePropose 错误地重新分配，C→6、D→7、E→8，seq 3 成为第一个永远投不出去的序号（Deliver 永远等 seq 3）。
- (乙) 乱序补发 (4,D)(3,C)(5,E) 下正确投递仍是 C、D、E（seq 3、4、5）。若按补发到达顺序投递，第 10 步三次 Deliver 依次投出 D、C、E——D 抢在 C 前，与全序 3<4<5 不符。
- (丙) 正确实现共投 5 条：A,B,C,D,E。若崩溃后 deliveredUpTo 回退为 0：重复投递 A、B，共投 7 条（A,B,A,B,C,D,E）。若跳过空洞从 nextSeq=6 继续：C、D、E 永久丢失，第 10 步三次 Deliver 全部投空（ok=false），共仍只有 2 条（A,B）。

## 三、四条不变量的保证位置与钉住测试

1. 无空洞前缀：Deliver 只投 `deliveredUpTo+1`（tob/tob.go `Deliver`），测试 `TestSequence`。
2. 与朴素重放一致：Crash 只删 `seq>deliveredUpTo`、RePropose 按原 seq 填回（`Crash`/`RePropose`），测试 `TestSelfCheck`（不变量 2 段）。
3. 序号单调：`seq.Counter.Allocate` 只增、`deliveredUpTo` 仅 Deliver 成功后 ++（tob.go），测试 `TestSelfCheck`（不变量 3 段）。
4. 失败不留痕：所有校验先于任何写操作（`Propose`/`RePropose` 入口），测试 `TestFaultInjection`。
