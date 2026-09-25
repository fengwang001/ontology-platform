# NOTES

## 八行分步表（C=4，初始四槽全 FREE、gen 全 0；槽状态记为 `O/F + gen`）

| # | 操作 | 涉及 connID 与世代 | 操作后槽 0/1/2/3 | 结果 |
|---|---|---|---|---|
| 1 | Open | 最小空闲 0，gen 0→1 | O1 F0 F0 F0 | Handle{0,1} |
| 2 | Open | 最小空闲 1，gen 0→1 | O1 O1 F0 F0 | Handle{1,1} |
| 3 | Open | 最小空闲 2，gen 0→1 | O1 O1 O1 F0 | Handle{2,1} |
| 4 | Close({1,1}) | 槽1 OPEN→FREE，gen 留 1 | O1 F1 O1 F0 | 成功，清空连接1数据 |
| 5 | Open | 最小空闲是 1（非 3），gen 1→2 | O1 O2 O1 F0 | Handle{1,2} |
| 6 | Recv(1,gen1,"old") | 槽1 OPEN 但 gen1≠当前2 | 不变 | ErrStale |
| 7 | Recv(3,gen1,"x") | 槽3 FREE（半开） | 不变 | ErrHalfOpen |
| 8 | Recv(1,gen2,"hi") | 槽1 OPEN 且 gen 匹配 | 不变 | 投递，连接1数据="hi" |

(甲) 不做世代隔离时第 6 帧 `"old"` 会被投递给 connID=1 上的**新连接**（gen2 那条）：旧连接的迟到帧混入新连接的累积数据，接收方把上一代连接的残留字节当作本连接数据——串流、数据污染且无法察觉。
(乙) 静默丢弃：对端以为帧已送达，连接看似存活实则数据凭空丢失，半开状态永远不可判定，对端只能空等或无谓重传。自动新建再投递：Recv 越权改变槽状态，违背「只有 Open 能分配槽、gen 只在 Open 时递增」——新连接的世代号无从定义，且抢占最小空闲槽会破坏 Open 的分配语义。
(丙) 单调递增不复用时第 5 步返回 **3**（next 只增不减）。长期后果：每个 connID 一生只用一次，C 个槽总共只能开 C 条连接，此后永远 ErrNoSlots——尽管槽实际空闲，connID 空间被快速耗尽。

## 四条不变量落点

1. 与朴素参照一致：判定规则集中在 `demux.Engine.Open/Close/Recv`（demux.go）逐步执行；由 `TestNaiveReference`（api/api_test.go，随机序列比对朴素模型）钉住。
2. 世代隔离：`demux.Recv` 中 `gen != 当前世代 → ErrStale` 分支，数据只按匹配 (id,gen) 追加；由 `TestStaleAndHalfOpen`、`TestConcurrentNoCrossTalk` 钉住。
3. 半开可判定：`demux.Recv` 中 `FREE → ErrHalfOpen` 分支，不丢弃也不误投；由 `TestStaleAndHalfOpen` 钉住。
4. 失败不留痕：demux.go 所有错误路径先校验后变更、写状态前 return；由 `TestFailureNoTrace` 钉住。
