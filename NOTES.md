# DRR 调度器设计笔记

## 第三节推导：flow 变空时赤字必须清零并移出活动表

- 不变量：flow 非空且结束本轮时队首块 > 赤字，而块 ≤ MaxBlock，故赤字 ∈ [0, MaxBlock−1]。
- 若变空后仍留在活动表（空闲期继续累积 quantum）：空闲 N 轮后赤字 = N×quantum，
  回归第一轮最多发 N×quantum + quantum 字节，随空闲时长无界增长 —— 即线上事故根因。
- 若只保留赤字但移出活动表：回归首轮最多 quantum + MaxBlock − 1，有界但白送历史欠账。
- 正确做法：发空即 deficit=0 并移出活动表（drr/drr.go Dequeue 发空分支）。回归首轮
  赤字从 0 起只加一次 quantum，发送 ≤ quantum ≤ quantum + MaxBlock − 1，与空闲时长无关。
- 测试钉住：TestAll/idle（X 空闲 1000 轮后回归首轮 ≤ quantum+MaxBlock−1；内联错误实现超界）。

## 第二节各条：代码位置与测试（测试均为 check/check_test.go 中 TestAll 的子测试）

1. 赤字规则：drr/drr.go Dequeue 内层 for（加 quantum、按队首 ≤ 赤字发送、不丢块）；测试 random。
2. 逐步一致：check/check.go Naive.Dequeue 每次线性扫描全部 flow；测试 random（种子 1，2000 步对拍）。
3. 字节公平：持续积压时每轮发送 = quantum + 赤字变化，赤字 ∈ [0, MaxBlock−1]，两轮次差 ≤ 1，
   故等权重 |Sa−Sb| ≤ quantum + 2×(MaxBlock−1)，与时长无关；测试 fair+checked（界 10238）。
4. 守恒：drr 中 sent/enqueued 与 flowq.Bytes() 计数；测试 concurrent 末尾逐 flow 断言。
5. 错误：drr/drr.go 的 ErrUnknownFlow/ErrBadSize/ErrFull（Enqueue 先校验后修改，零副作用）；
   测试 errors（errors.Is 逐条断言 + 填满 MaxQueued 后 ErrFull 且计数不变）。

## 第四节实测（10000 空闲 flow + 2 活跃 flow，400 次 Dequeue）

- 单次 Dequeue 检查 flow 数：最大 2，摊还 1.33（要求 ≤ 3）；空闲 flow 不在活动表，不被扫描。
- 断言位置：fair+checked 中每次 Dequeue 后检查 Stats().Checked ≤ 3。
