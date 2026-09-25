# NOTES

## 三、推导：K=3，key="a"，Seq 顺序 10,8,12,5,8,11,9,7

| 步 | Seq | 事后 high | 判定 | 事后 Accepted | 事后 Dropped |
|---|---|---|---|---|---|
| 1 | 10 | 10 | 首个接受（high 原为"无"） | 1 | 0 |
| 2 | 8  | 10 | 窗口内接受（窗口[7,10]，不推进） | 2 | 0 |
| 3 | 12 | 12 | 窗口外上方，Seq>high，接受并推进 | 3 | 0 |
| 4 | 5  | 12 | 超窗丢弃（窗口[9,12]，5<9，high 不变） | 3 | 1 |
| 5 | 8  | 12 | 超窗丢弃（8<9，high 不变） | 3 | 2 |
| 6 | 11 | 12 | 窗口内接受（不推进） | 4 | 2 |
| 7 | 9  | 12 | 窗口内接受（9==high-K 左闭边界） | 5 | 2 |
| 8 | 7  | 12 | 超窗丢弃（7<9） | 5 | 3 |

- (甲) 第 7 步 9==high-K=9：左闭，**接受**。若误写 `Seq > high-K`（左开），第 7 步被丢弃 → 最终 Accepted 错成 **4**（正 5），Dropped 错成 **4**（正 3）。
- (乙) 第 4 步丢弃后若把 high 覆盖成 5：第 5 步 Seq=8 满足 8>5，会被**接受并把 high 推到 8**；按此错误规则继续（6:11 接受 high=11；7:9 在[8,11]内接受；8:7 被丢），最终 Accepted 错成 **6**（正 5），Dropped=2。
- (丙) K=3，序列 ("a",100)、("b",1)：per-key 下 b 是首事件无条件接受，Accepted 总数=2、Dropped=0。全局单 high（=100，窗口[97,100]）会把 ("b",1) 错判为超窗丢弃 → Accepted 错成 1、Dropped 错成 1。违反**第二节不变量 1**（与朴素 per-key 逐条重算不一致；也破坏首事件规则）。

## 二、四条不变量的代码落点与钉住测试

1. 与朴素逐条重算一致：`wcount.Feed` 对每条事件调用 `slack.Decide`，按其判定逐条改状态（wcount/wcount.go）；测试 `TestNaiveEquivalence`（多随机序列逐 Key 比对 Accepted/High 与总 Dropped）。
2. 窗口边界自洽、high 只进不退：左闭条件 `seq >= high-K` 在 slack/slack.go 的 `Decide`；丢弃分支在 wcount.go 中不写 high；测试 `TestWindowBoundaries`（high-K 接受、high-K-1 丢弃、丢后 high 不变、乱序不回退）。
3. 累计只增：wcount.go 中只有 `accepted[key]++` 与 `dropped++` 两处自增，无任何回退赋值；测试 `TestCountersMonotonic`。
4. 失败不留痕：wcount.go `Feed` 先整批校验（空 Key、批内新增去重后 Key 数是否超 maxKeys），全部通过后才在同一把锁内落状态；测试 `TestFeedAtomicity`（拒后三态不变且实例可继续使用）。
