# NOTES
## 一、十一步推导（maxBuffered=3, timeout=2；缓冲记 seq:value）
|步|now|next|缓冲|lastAdvance|发出|丢失|报错|
|1|0|2|—|0|1|无|无|
|2|0|2|{3:c}|0|无|无|无|
|3|0|2|{3:c,5:e}|0|无|无|无|
|4|1|2|{3:c,5:e}|0|无|无|无|
|5|2|4|{5:e}|2|3|2|无|
|6|2|4|{5:e}|2|无|无|Seq 过期|
|7|2|6|—|2|4,5|无|无|
|8|2|6|{8:h}|2|无|无|无|
|9|2|6|{8:h,9:i}|2|无|无|无|
|10|2|6|{8:h,9:i,10:j}|2|无|无|无|
|11|2|6|{8:h,9:i,10:j}|2|无|无|缓冲溢出|
- 甲：第5步 now=2，2-0>=2 触发；丢失 2、发出 3(c)，next=4、lastAdvance=2。若误写严格 >：2>2 为假不触发，next 仍 2、2 不入丢失；连锁使第6步 Feed(2,b) 不再报过期，而是命中发出 2 并级联发出 3。
- 乙：发出 4 后必须级联发出 5(e)；若只发到达的这条，5(e) 滞留缓冲、next 停在 5。
- 丙：正确为报「缓冲溢出」且缓冲仍 {8,9,10}；若先加入再判超界，报错后残留 {8,9,10,11}（实际四条，已破界）。
## 二、不变量保证位置与钉住测试
1. 朴素重放一致：reorder.go 中 emit/cascade 与 Tick 超时 flush 只按 next 顺序追加发出，TestNaiveReplayRandom 钉住。
2. 顺序严格递增：每条发出都经 emit()，且缓冲弹出仅在 Seq==next 时发生（reorder.go），TestEmittedStrictlyIncreasing 钉住。
3. 缓冲不超界：Gap 分支在加入前判断 pq.Len()>=maxBuffered（reorder.go Feed），TestBufferBound 钉住。
4. 失败不留痕：四类错误均在任何状态写入之前返回（reorder.go New/Feed），TestRejectionsLeaveNoTrace 钉住。
