下行指令排程器设计说明（wake / cmdq / sched）

1. 分层：sched 为唯一外观，持有设备表、全局参数(K,Bw,R,Q)与一把互斥锁，
   串行化所有操作；wake 只管窗口参数与换参，cmdq 只管指令生命周期。
2. 窗口参数最多保留两代：prev 仅在换参当窗有效（字段 start/end 固定），
   cur 自 eff 起按 (P,o,w) 计算。再换参时若旧 cur 窗跨过 now 则降为
   prev（end=窗口终点），否则丢弃。放弃立即切换：跨 e 的新窗整窗作废，
   让设备已醒来的旧窗走完，额度归属无歧义。
3. 候选选择：先重投（eligible，按 firstSend 升序），后未投递（prio 降、
   seq 升）。放弃"跳过放不下的大指令取小的"：协议语义即队首阻塞，跳过会
   让结果依赖后续指令大小、且改变优先级语义。
4. 失败时机：投递次数在第 R 次投递所在窗口之后的第一次 Deliver 才检查，
   即"第 R 次投递后的失败时机"；Ack 在失败判定前，故该窗内仍可确认。
5. examined 上界的数据结构：eligible 用按 firstSend 的有序集合；优先级
   候选为 4 个惰性堆（过期/失败弹出不计 examined）；换窗迁移只做指针级
   搬迁。Deliver 因首个放不下即停，真正考察 ≤ 投递数+过期数+失败数+1。
6. 过期：按 expire 建惰性小顶堆，任何入口先 purge；拒绝操作在副本上判定
   （Enqueue 先做全部检查后落盘），Ack/Deliver 的状态修改只发生在错误
   检查之后，保证被拒操作不留任何副作用（含过期清除）。
7. 拒绝次序在 sched 统一：ErrInvalid > ErrClockBack > ErrNoDevice/
   ErrExists > ErrDupCmd > 操作自身状态错误。
8. seq 每设备单调递增；窗口以起点 int64 标识；全部时间用 int64，不溢出。
9. 放弃按窗口预分组/预排序投递结果的方案：会放大 examined 且使同窗多次
   Deliver 的额度共享难以精确；改为现取现判。
10. 本地验证：go build ./...；go test -v -race ./...（表驱动用例 +
    1500 组随机操作序列与逐步朴素模拟逐条对照，-v 日志含输入/输出/判定
    依据）；100 与 10000 队列档对照 examined 上界；gofmt -l . 与
    go vet ./...。
