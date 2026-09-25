# 双流水位对齐器 NOTES

## 一、八步推导（A1 表示 A 流 TS=1；-∞ = 该流尚未见事件）

| 步 | 事件 | wA | wB | W | 判定 | 本步发出 | 滞留缓冲 |
|---|---|---|---|---|---|---|---|
| 1 | A 1 | 1 | -∞ | -∞ | 接受·缓冲 | 无 | A1 |
| 2 | A 2 | 2 | -∞ | -∞ | 接受·缓冲 | 无 | A1 A2 |
| 3 | B 1 | 2 | 1 | 1 | 接受·发出 | A1 B1 | A2 |
| 4 | A 3 | 3 | 1 | 1 | 接受·缓冲 | 无 | A2 A3 |
| 5 | B 2 | 3 | 2 | 2 | 接受·发出 | A2 B2 | A3 |
| 6 | B 5 | 3 | 5 | 3 | 接受·缓冲 | A3 | B5 |
| 7 | A 4 | 4 | 5 | 4 | 接受·发出 | A4 | B5 |
| 8 | A 2 | 4 | 5 | 4 | 迟到丢弃 | 无 | B5 |

Close 后 W=+∞，发出 B5。总输出 A1 B1 A2 B2 A3 A4 B5，丢弃 1 条。

(甲) 步6 W=min(3,5)=3，只发 A3。若误用 max(wA,wB)=5：缓冲中 B5（5≤5）也被发出；步7 A4 到达时 W=5 仍发出 → 输出 …B5,A4，5 后接 4 违反非降——A 流才推进到 3，B5 必须滞留。
(乙) 步1 W=-∞（B 未见），A1 被缓冲不发出。若把未见流当 +∞：W=min(1,+∞)=1，A1 第 1 步即被发出；一般情形（如 A5 先到、B1 后到）会输出 5 再 1，违反非降。若当 0：TS=0 的首事件会被立即发出，同样违反「不提前发出」。
(丙) 步8 A2：2<wA=4 → 迟到丢弃。若不丢弃而缓冲：Close 时它与 B5 一起按 TS 升序发出，输出末尾 …A4,A2,B5——多出一条 A2，4 后接 2 违反非降，且 TS=2 重复出现。

## 二、四条不变量：保证位置与钉住测试

1. 与朴素重排一致：align.drain 用 (TS,到达序) 最小堆逐个弹出、Close 把两水位推 +∞ 全放，故全序=TS 升序、同 TS 按到达序；api.Feed/Close 累积 emitted。测试 TestNaiveConsistency。
2. 顺序非降：wm.Advance 只取 max ⇒ wA/wB 单调不减 ⇒ W 单调不减，且堆弹出本身按 TS 升序。测试 TestNaiveConsistency（内含非降断言）。
3. 不提前发出：align.drain 循环条件「队首.TS ≤ W」，任一流未见时 W 不存在直接返回。测试 TestNoPrematureEmission。
4. 失败不留痕：api.Feed/Close 先校验（ErrBadStream/ErrNegativeTS/ErrClosed）后动状态，校验失败直接返回。测试 TestRejectedNoStateChange。

复杂度：定位最小 TS 用 container/heap 最小堆，单次弹出 O(log m) 次比较，不随 m 线性增长；非导出字段 align.Aligner.lastCmps 记录最近一次 drain 的 Less 调用数，仅包内测试 TestLocateMinComparesBounded（m=100…10000，阈值 64）可读，不经任何导出接口暴露。
