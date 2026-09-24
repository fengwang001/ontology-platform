# CDC 维表查找缓存失效 — 推导与不变量
## 第三节：十四步分步表（源头 / fence / 缓存 / 回填 / Get / SourceReads）
| # | 步骤 | 源头 | fence | 缓存 | 回填 | Get | reads |
|---|------|------|-------|------|------|-----|-------|
| 1 | Update(k,"a") | a@1 | 0 | 无 | — | — | 0 |
| 2 | Deliver(全部) | a@1 | 1 | 无 | — | — | 0 |
| 3 | R1=BeginRead | a@1 | 1 | 无 | — | — | 1 |
| 4 | Update(k,"b") | b@2 | 1 | 无 | — | — | 1 |
| 5 | R2=BeginRead | b@2 | 1 | 无 | — | — | 2 |
| 6 | FinishRead(R2) | b@2 | 1 | b@2 | 接受(floor=1) | — | 2 |
| 7 | FinishRead(R1) | b@2 | 1 | b@2 | 拒绝(floor=2) | — | 2 |
| 8 | Deliver(全部) | b@2 | 2 | b@2(保留) | — | — | 2 |
| 9 | R3=BeginRead | b@2 | 2 | b@2 | — | — | 3 |
| 10 | Delete(k) | 删@3 | 2 | b@2 | — | — | 3 |
| 11 | Deliver(全部) | 删@3 | 3 | 无 | — | — | 3 |
| 12 | FinishRead(R3) | 删@3 | 3 | 无 | 拒绝(floor=3) | — | 3 |
| 13 | Get(k) | 删@3 | 3 | 负@3 | 接受(floor=3) | 不存在(未命中) | 4 |
| 14 | Get(k) | 删@3 | 3 | 负@3 | — | 不存在(命中) | 4 |

**(甲)** 步7 floor=max(fence1,条目2)=2，R1@1<2 拒绝；步12 floor=max(fence3,无)=3，R3@2<3 拒绝。若不查版本一律接受：步7 后缓存被回写成 a@1；步8 事件 v2>1 会删掉它 → 能纠正（暂时性陈旧，纠正事件仍在途中）。步12 后缓存被写成 b@2，步13/14 Get 命中返回 "b"（源头实已删除）→ 永久性陈旧：失效事件（步11）已先于回填到达，此后再无事件能纠正它。本质区别：前者还有在途事件兜底，后者兜底已用完。

**(乙)** 步11 后 fence 被清为 0；步12 floor=max(0,0)=0，R3@2≥0 被接受 → 缓存 b@2；步13/14 Get 命中返回 "b"（错误，源头已删），永久陈旧。丢 fence 等于忘记「已经知道 v3」，把老数据当新数据收下。

**(丙)** 接受（规则是 ≥floor）。若误写成 >floor：步13 回填（v3=floor3）被拒、缓存仍无条目，步13 Get 返回「不存在」但未命中；步14 再次未命中又回源，SourceReads=5（正确为 4），此后每次 Get 都回源。步8 事件 v2 等于条目 v2：条目保留（仅当条目版本 < 事件版本才删）。

## 第二节：四条不变量 → 代码位置 → 钉住它的测试
1. 参照一致：`api.Get` 未命中即 BeginRead+FinishRead 回源；`lcache.Backfill` 用 floor 拒陈旧回填 → `TestInvariantReference`
2. 栅栏不被越过：`lcache.Apply` 仅 v>fence 时升 fence 并删低版本条目；`Backfill` floor 取 max(fence,条目版本) → `TestInvariantFence`
3. 不回退：`Backfill` 的 floor 含现有条目版本，低于 floor 直接拒写 → `TestInvariantNoRegression`
4. 失败不留痕：`src.Delete` 先查存在性再改；`api.Deliver` 先 WouldExceed 后 Drop；`api.Get` 先查限额再读源；`FinishRead` 出错不作废令牌 → `TestFailureAtomicity`
