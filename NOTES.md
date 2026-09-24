# NOTES

七步推导，N=3（slot=首次进入序号，单调递增，驱逐不复用）：

| 步 | 事件 | 集合内 key:(lastSeen,slot) | 驱逐 | Count |
|---|---|---|---|---|
| 1 | Track(A,10) | A:(10,0) | - | 1 |
| 2 | Track(B,10) | A:(10,0) B:(10,1) | - | 2 |
| 3 | Track(C,20) | A:(10,0) B:(10,1) C:(20,2) | - | 3 |
| 4 | Track(A,5)  | A:(5,0) B:(10,1) C:(20,2) | - | 3 |
| 5 | Track(D,10) | B:(10,1) C:(20,2) D:(10,3) | A:(5,0) | 3 |
| 6 | Track(E,10) | C:(20,2) D:(10,3) E:(10,4) | B:(10,1) | 3 |
| 7 | Track(F,20) | C:(20,2) E:(10,4) F:(20,5) | D:(10,3) | 3 |

(甲) 第6步 B、D 的 lastSeen 并列=10，按较小 slot 驱逐 B(10,1)。错按较大 slot 会驱逐 D，错得 B:(10,1) C:(20,2) E:(10,4)；正确为 C:(20,2) D:(10,3) E:(10,4)。
(乙) 第5步正确留 B:(10,1) C:(20,2) D:(10,3)；错写成驱逐最大者会踢 C:(20,2)，错留 A:(5,0) B:(10,1) D:(10,3)。
(丙) 七步后 Count=3。维护"历史不同键总数"会错返回 6（A..F 共 6 个），违反规则原话："`Count()` 返回当前集合大小，**恒等于**集合内键数，永不超过 N"。

不变量落点（代码位置 / 钉住测试）：

1. 容量：pool.Track 满则先 heap.Pop 驱逐再 Push；Count 直接取堆大小。TestNaiveEquivalence、TestConcurrent。
2. 朴素参照一致：rk.Less 定 (lastSeen,slot) 序，pool 用索引最小堆与槽位计数；TestNaiveEquivalence（随机序列逐键比对成员与 lastSeen/slot）。
3. 计数不虚增：已存在键只 heap.Fix 不增删，驱逐再入走新 slot；Count()==len(Snapshot()) 逐步断言。TestCountEqualsSnapshot、TestEvictedRetrackNewSlot。
4. 失败不留痕：api.Track/New 在触碰任何状态前返回互异哨兵错误；TestRejectedNoTrace、TestSentinelErrors。
