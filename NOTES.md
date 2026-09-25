# NOTES

## 第三节推导：NewPhaser(2)（A=0、B=1），A.Arrive → Register C(id=2) → B.Arrive → C.Arrive

| # | 操作 | 相位 | party 集合 | 未到达集合 |
|---|---|---|---|---|
| 1 | A Arrive | 0 | {0,1} | {1} |
| 2 | Register→C=2（加入当前相位） | 0 | {0,1,2} | {1,2} |
| 3 | B Arrive | 0 | {0,1,2} | {2}（未空，不推进） |
| 4 | C Arrive | 1 | {0,1,2} | {0,1,2}（降0→推进，全体重置） |

- **(甲)** 第 3 步后**不推进**（C 未到），第 4 步后才推进到 1。若 Register 让新 party 加入**下一相位**：第 3 步 B 到达后相位 0 的未到达即降 0 → **提前推进到 1**；第 4 步 C 正在到达的是相位 **1**。正确实现 C 到达相位 **0**（Arrive 返回 0），错误实现返回 1。
- **(乙)** 先 B.Arrive（未到={A}），再 A.ArriveAndDeregister。正确=**先到达**：相位 0 计数降 0→推进 1、重置 {A,B}，再移除 A，结果相位 **1**、parties {B}。先注销后到达：注销时 A 尚未到达，删 A 把未到达降 0，**额外推进一次**到 1；随后的 Arrive 在相位 1 又触发一次→相位 **2**（且对已注销 id 做 Arrive 本应报未知 id）。差一次多余推进。
- **(丙)** B 是最后到达者，Arrive 返回**推进前**相位 **0**，与 A 的返回值一致（都是 0）。若返回「推进后的当前相位」，B 返回 **1**（off-by-one），A=0、B=1 互相不一致。

## 第二节四条不变量的代码落点与钉住测试

1. **与朴素参照一致**：`api/api.go` 的 `SelfCheck`（内置操作序列逐步比对返回值、错误身份、相位/两集合快照）与同文件 `naiveRef`（一把 `sync.Mutex` 的朴素参照）；钉住测试 `TestNaiveReferenceEquivalence`（50 组随机序列）+ `TestSelfCheck`（`api/api_test.go`）。
2. **推进条件精确**：`phase/phase.go` 的 `Arrive`：`delete(unarrived,id)` 后只判 `len(unarrived)==0 && len(parties)>0`；钉住测试 `TestAdvancePrecision`（四步中间态断言 B 到达后不提前推进 + 乙场景相位为 1）。
3. **相位单调**：全仓库唯一推进点是 `phase/phase.go` `Arrive` 里的 `s.phase++`（每次恰好 +1，终止态单独置 -1）；钉住测试 `TestPhaseMonotonic`（乱序到达 5 轮，每轮 +1）。
4. **失败不留痕**：`phase/phase.go` 所有错误返回（`ErrBadN`/`ErrTerminated`/`ErrUnknownParty`/`ErrDuplicateArrival`）全部位于任何 map 增删之前，`ArriveAndDeregister` 先调 Arrive 出错即原样返回；钉住测试 `TestRejectedOpsLeaveNoTrace`（表驱动，拒绝前后 Snapshot 深相等）。
