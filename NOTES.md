# 异步检查点屏障快照 — 推导与不变量

C=2，八事件按全局顺序到达（缓冲格式 (ch,delta)）：

| 步 | 事件 | sum | 在途缓冲 | 已阻塞 | 快照 |
|---|---|---|---|---|---|
| 1 | Rec(0,+5)  | 5  | — | {} | — |
| 2 | Rec(1,+10) | 15 | — | {} | — |
| 3 | Bar(0,1)   | 15 | — | {0} | — |
| 4 | Rec(0,+7)  | 15 | [(0,+7)] | {0} | — |
| 5 | Rec(1,+3)  | 18 | [(0,+7)] | {0} | — |
| 6 | Bar(1,1)   | 25 | —（先快照后排空） | {} | snap[1]=18，随后放 +7 |
| 7 | Rec(0,+2)  | 27 | — | {} | — |
| 8 | Rec(1,+1)  | 28 | — | {} | — |

(甲) snap[1]=18。若在途记录先入状态再快照：第 4 步 +7 已提前入，snap[1] 错成 25。
(乙) 第 3 步见首个屏障即快照：snap[1] 错成 15；被错误排除的是第 5 步 Rec(1,+3)（在通道 1 屏障之前到达，+3）。
(丙) 阻塞期记录直接丢弃：全流终值错成 21（=28−7）；丢失第 4 步 Rec(0,+7)，delta=+7。

## 不变量：代码保证位置 / 钉住的测试

1. 快照=批量参照：snap/snap.go 的 completeLocked 在 blocked==C 时先写 snaps[id]=sum，再逐条放在途缓冲；TestInvariantSnapshotMatchesBatchReference（含随机交错多档）。
2. 恰好一次：snap/snap.go 的 Apply Rec 分支使每条 Rec 要么立即入 sum、要么进 align.Channel 在途缓冲，completeLocked 中每条仅放出一次后 Reset；TestInvariantExactlyOnce。
3. 屏障只栅自己通道：Apply Rec 分支只看本通道 align.Channel.Blocked()，未阻塞通道立即累加，completeLocked 先快照后排水，故对齐期 sum 不含任何在途 delta；TestInvariantBarrierBlocksOnlyOwnChannel。
4. 失败不留痕：api.Feed 持写锁转调 snap.Apply；后者先按通道 lastID（批内用 map 模拟）整批校验、零修改，全合法才进入应用阶段；TestRejectedFeedLeavesNoTrace、TestFeedAtomicity、TestSentinelErrorsDistinct。
