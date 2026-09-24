# ontology-421 推导与不变量

## 七步表（memCap=2；ts=单调访问时刻；读盘=累计磁盘读数）

| # | 操作 | 内存(key@ts) | 磁盘 | 读盘 | 返回 |
|---|---|---|---|---|---|
| 1 | W(A,1) | {A@1} | {A:1} | 0 | — |
| 2 | W(B,2) | {A@1,B@2} | {A:1,B:2} | 0 | — |
| 3 | R(A) | {B@2,A@3} | {A:1,B:2} | 0 | 1 |
| 4 | W(C,3) | {A@3,C@4} | {A:1,B:2,C:3} | 0 | —（换出 B@2） |
| 5 | R(B) | {C@4,B@5} | {A:1,B:2,C:3} | 1 | 2（换出 A@3） |
| 6 | W(B,20) | {C@4,B@6} | {A:1,B:20,C:3} | 1 | — |
| 7 | R(B) | {C@4,B@7} | {A:1,B:20,C:3} | 1 | 20 |

- (甲) 第4步换出 **B**（ts=2 最小）。若误换最近使用者则换出 **A**；第5步 Read(B) 命中内存、不读盘，磁盘读数错成 **0**（正解 **1**）。
- (乙) 第7步返回 **20**。若 Write 只更磁盘不更内存，Read 命中旧缓存错得 **2**。
- (丙) Read(D) 返回 **("", false)**（不存在）。若错成零值返回 **("", true)**，即把空串当真实值、谎报 key 存在。

## 不变量落点

1. 透明读写：`store.Read` 热命中刷新时间、冷命中以权威 disk 取值并提升（store.go 75-90）—— 钉于 `TestSevenStepTable`（第3/5/7步返回值与 Read(D)）与 `TestReplayAndWriteThrough`（随机重放逐 op 比对）。
2. 写穿正确：`store.Write` 先 `disk[k]=v` 再动缓存（store.go 61-64）—— 钉于 `TestReplayAndWriteThrough` 每次循环后断言 disk 等于重放参照、绝无旧值。
3. LRU 精确：`tier` 的 (At,Key) 最小堆根即换出候选（tier.go），`store.admit` 超限只 Pop 堆根（store.go 48-58）—— 钉于 `TestSevenStepTable`（第4步换 B）、`TestReplayAndWriteThrough`（热集==最近访问的 min(cap,总数) 个）；O(1) 取最小钉于 `TestEvictionScannedIsConstant`（m=100/1000/10000 探针恒为 1）。
4. 失败不留痕：`api.New` 拒非法容量，`api.Write` 五项校验全部通过后才调底层（api.go）—— 钉于 `TestRejectedOpsAtomic`（拒绝后热集/总数/读盘数不变且仍可读写）、`TestSentinelsDistinct`、`TestOverwriteNeverHitsMaxKeys`。

`Store.SelfCheck` 内置七步+大 m 探针+五类拒绝核验，只回 error 不回探针数值 —— 钉于 `api.TestSelfCheck`；并发 `go test -race` 钉于 `store.TestConcurrentReads`。
