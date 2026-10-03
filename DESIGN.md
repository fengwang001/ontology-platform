# 工作流活动执行器设计说明

## 结构
- retry：Policy(M,d0,cap) 校验；Backoff(k)=min(d0*2^(k-1),cap)；CanRetry(k)=k<M。
- deadline：Kind(s2s/s2c/hb/sc/wake) 到期项与最小堆；同刻按 sc、s2c、hb、s2s 排序；wake（退避结束）仅内部驱动，优先级最低之外单列。
- activity：Executor 管理多个活动，全局单调时钟；每个活动含状态机 Scheduled/Running/Waiting/终局、尝试号 k、排队 g、开始 r、最近心跳 h、进度 progress、t0 与终局信息。

## 到期推演
- 每次操作在活动副本上先做到期处理：堆顶 ≤ now 即弹出，取最早一项作为原因，失败时刻=到期时刻本身（恰等即到期），处理时刻不影响结果。
- Waiting 时只保留 sc 与 g' 的 wake 项；到 g' 自动转为第 k+1 次 Scheduled（Waiting 期间 k 已为 k+1），并重建该状态堆，一次调用可跨多次尝试。
- Status 只读：投影不提交，不推进时钟；成功的写操作才把时钟推到 now。被拒操作零副作用，结果是 now 的纯函数。

## 关键取舍
- 不再为注定超时的重试排队：失败后若 g'=f+退避 ≥ t0+sc，直接终局 Failed(ρ)（时刻 f），不进入 Waiting。理由：该次排队不可能在总时限内被领取/执行，进入 Waiting 只会产生一个时刻为 g' 的 TimedOutSC，掩盖真正的失败原因 ρ 与时刻 f；直接终败使“超时类别、失败时刻、重试次数”可精确复现，也省一次无意义堆周期。
- s2s（排队到开始）超时不重试：活动始终未被领取，重试同一调度延迟无新信息，通常意味着容量/调度系统性故障；s2c 是拿到执行权后执行超时，重试可能落在健康执行者上，故消耗一次重试预算。
- 放弃方案：每类超时（含 s2s、sc）都消耗重试预算。它会让 sc 到期后仍反复重排队，违背“总时限”语义；s2s 反复重试只会在同一 deadline 下连环超时。故 s2s 立即终局、sc 立即终局，仅 s2c/hb/app 可重试。

## 拒绝优先级
参数/配置 → ErrClock → 不存在 → ErrStale(k 不符) → ErrTerminal(k 符但已终局) → ErrState(状态不符)。到期当刻的操作先被到期处理超越（Complete 在当刻对已终局为 ErrTerminal，旧尝试号为 ErrStale）。

## 验证
- `go test ./...`、`go test -race ./...`、`gofmt -l . && go vet ./...`。
- 示例、四类到期恰等与差 1、同刻次序、cap、g'=sc、s2s 不重试、跨多次尝试、ErrStale/ErrTerminal、进度传递；随机用例与逐事件朴素模型对照；非导出计数器断言每次操作查看堆项数 ≤ 到期事件数+1。
- 日志打印每次操作的输入、到期推演依据与输出（Executor.SetLogger）。
