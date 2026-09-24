# NOTES

## 八行分步表（C=2，值为该事件之后）

| # | 事件 | sum | 在途缓冲 | 已阻塞 | 快照 |
|---|---|---|---|---|---|
| 1 | Rec(0,+5) | 5 | — | ∅ | — |
| 2 | Rec(1,+10) | 15 | — | ∅ | — |
| 3 | Bar(0,1) | 15 | — | {0} | — |
| 4 | Rec(0,+7) | 15 | [(0,+7)] | {0} | — |
| 5 | Rec(1,+3) | 18 | [(0,+7)] | {0} | — |
| 6 | Bar(1,1) | 25 | — | ∅ | snap[1]=18（先快照后排空 +7） |
| 7 | Rec(0,+2) | 27 | — | ∅ | — |
| 8 | Rec(1,+1) | 28 | — | ∅ | — |

- (甲) snap[1]=18；若在途先入状态再快照，会错成 **25**（多算 +7）。
- (乙) 第 3 步就快照会错成 **15**；被排除的是 **Rec(1,+3)**（第 5 步，值 3）。
- (丙) 丢弃在途则终值错成 **21**；丢失 **Rec(0,+7)，数值 7**。

## 四条不变量：保证位置 / 钉住的测试

1. 与批量参照一致：`snap/snap.go` 的 `complete` 先写 `snaps[id]=sum` 再 `Drain` 排空；`TestBatchReference`。
2. 恰好一次：`snap/snap.go` 的 `applyRec`（阻塞则 `Buffer`，否则 `sum+=delta`）与 `complete` 各只入一次；`TestExactlyOnce`。
3. 屏障只栅自己通道：`applyRec` 只查本通道 `Blocked()`，第 5 步 sum=18；`TestBarrierGatesOwnChannel`。
4. 失败不留痕：`Engine.Feed` 先整批校验通过后才落状态，四个哨兵错误；`TestRejectedBatchAtomic`、`TestSentinelErrorsDistinct`。
