# 有界乱序重排缓冲器 NOTES

## 十一步分步表（maxBuffered=3, timeout=2；初始 now=0, next=1, lastAdvance=0）

| 步 | 操作 | now | next | 缓冲 | lastAdv | 发出 | 丢失 | 报错 |
|---|---|---|---|---|---|---|---|---|
| 1 | Feed(1,a) | 0 | 2 | {} | 0 | 1 | 无 | 无 |
| 2 | Feed(3,c) | 0 | 2 | {3} | 0 | 无 | 无 | 无 |
| 3 | Feed(5,e) | 0 | 2 | {3,5} | 0 | 无 | 无 | 无 |
| 4 | Tick | 1 | 2 | {3,5} | 0 | 无 | 无 | 无 |
| 5 | Tick | 2 | 4 | {5} | 2 | 3 | 2 | 无 |
| 6 | Feed(2,b) | 2 | 4 | {5} | 2 | 无 | 无 | Seq过期 |
| 7 | Feed(4,d) | 2 | 6 | {} | 2 | 4,5 | 无 | 无 |
| 8 | Feed(8,h) | 2 | 6 | {8} | 2 | 无 | 无 | 无 |
| 9 | Feed(9,i) | 2 | 6 | {8,9} | 2 | 无 | 无 | 无 |
| 10 | Feed(10,j) | 2 | 6 | {8,9,10} | 2 | 无 | 无 | 无 |
| 11 | Feed(11,k) | 2 | 6 | {8,9,10} | 2 | 无 | 无 | 缓冲溢出 |

(甲) 第5步 now=2，now-lastAdvance=2>=2 超时触发：把 [2,3) 记丢（丢 2），next 跳到 3 并发出 3，缓冲剩 {5}。若误写成严格 `>`：2>2 不触发，2 不丢、next 停在 2；连锁到第 6 步 Feed(2,b) 会被当作命中而**错误地发出 2 并级联发出 3**（本应报「Seq 过期」）。
(乙) 第7步发出 4 后还须级联发出缓冲里的 5（5==next）。若忘级联：5 滞留缓冲，next 停在 5。
(丙) 第11步应先判超界再入缓冲：报「缓冲溢出」且缓冲仍 {8,9,10}。若「先加入再判」，报错后缓冲残留 {8,9,10,11}（4 条，已越界）。

## 四条不变量的保证位置与钉住测试

1. 朴素重放一致：emit 只在 s==next 时发生、flush 只跳过已记丢区间（reorder.go emit/cascade/Tick）；测试 TestNaiveReplay。
2. 顺序严格递增：next 单调不减且仅 emit 时 +1（reorder.go emit）；测试 TestStrictlyIncreasing。
3. 缓冲不超界：Gap 分支在加入前判 `len>=maxBuffered`（reorder.go Feed）；测试 TestBufferBound。
4. 失败不留痕：全部校验先于任何状态写（reorder.go Feed 开头、api.New）；测试 TestFailureAtomic。
