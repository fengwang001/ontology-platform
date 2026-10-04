# 流水线运行调度器设计说明

## 包结构
- `group`：运行记录 `Run`/`State`，以及每组「占位者 + 一个 Pending」的两张表（有组名 / 空组名各一张）。
- `slot`：全局执行位（容量 C，仅记占用数）与 FIFO 等待队列。队列用环形缓冲实现，配合组表可 O(1) 任意移除（顶掉 Waiting 不扫描）。
- `sched`：一把互斥锁串行化全部公开操作；操作内按规则改 `group`/`slot`，末尾统一做执行位分配。锁内即线性化点，天然满足并发等价串行。

## 关键取舍
- **Cancelling 继续占用执行位与占位者身份**：取消只是「已发请求、尚未确认」，在 AckCancel/Finish 前运行仍在执行；若立即释放，Pending 会与未确认运行并发，违背「每组至多一个占位者」的语义，也无法表达迟到 Finish 被取消覆盖。
- **Pending 晋升后排队尾，按「成为占位者时刻」排序**：队列次序是占位权次序而非提交次序（范例 r5 先于 r4）。放弃继承原提交次序的方案，否则需要为每个运行维护虚拟排队时刻并在队列中重排。
- **队列上限用「模拟后净增」判定**：Submit 前先算 L0，完成入参与分配后得 L1，仅当 L1>L0 且 L1>Q 才整体回滚（拒绝时连旧 Pending 顶替代为 Superseded 也撤销）。实现上只需预判：唯一会净增的情形是「新占位者入队且队满（busy==C）」；顶替 Waiting 是一出一入净增 0，Pending 从不过队列门。
- **晋升不受 Q 约束**：终态释放位置后先让本组 Pending 入队尾（可能暂时超 Q），再统一分配；Q 只约束 Submit 的净增。
- **四类错误用哨兵错误**（ErrInvalid、ErrNotFound、ErrState、ErrQueueFull），`errors.Is` 可判；校验次序：参数 > 不存在 > 状态不符，Submit 为 参数 > 队列满。
- **Finish 对 Cancelling 一律 Cancelled**：取消优先，ok 丢弃；Cancel 对 Cancelling 重复调用报 ErrState。
- **protected 只影响 Submit 顶替 Running**，不影响 Waiting，也不挡用户显式 Cancel。

## touched 计数
`Run` 每次被读写计数 +1（同一次操作内同一条记录多次触达按实际触达计）。
Submit 至多：新运行 + 旧 Pending + 旧占位者 +（队首）= 4；Finish/AckCancel：目标 + 本组 Pending + 队首 = 3；Cancel：目标 +（Pending/队首）≤ 2。
故取消队列中部 Waiting 必须 O(1) 移除，由 `slot.Remove` 配合 ring 索引完成。测试在队列长度 100 与 10000 两档对照断言上界。

## 本地验证
`go build ./... && go vet ./... && go test -race -count=1 ./...`；
随机对照 1500 组操作序列与逐步朴素模拟器逐操作比对（输入、输出、状态、touched、判定依据均入日志）。
