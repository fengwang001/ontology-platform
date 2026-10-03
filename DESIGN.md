# 工作流活动执行器设计说明

## 包划分
- `retry`：纯函数。退避 `Backoff(k)=min(d0*2^(k-1), cap)`；`CanRetry(k,M,gp,t0,sc)` 判定预算与总时限。
- `deadline`：`Config`（s2s/s2c/hb/sc）、到期事件类型（sc>s2c>hb>s2s 定序）、按时刻最小堆 `Heap`（Set 固定项 / Pop / Peek，惰性探测计数）。
- `activity`：单活动状态机（Waiting/Scheduled/Running/终局）+ `Executor`（全局时钟、活动表、互斥）、错误哨兵、输入输出日志。

## 到期与推演
- 每个非终局状态的堆只含该状态的到期项：Scheduled{s2s,sc}、Running{s2c,hb,sc}、Waiting{sc}。
- 每项操作先在虚拟副本上按 now 推演：循环取堆顶，`t>now` 即停（故一次处理考察堆项数 = 到期事件数 + 1）；恰等到期立即处理。
- Waiting 不走堆：直接与等待结束时刻比较，避免唤醒事件虚增堆探测次数。
- 到期时刻即失败时刻；同刻按 sc、s2c、hb、s2s 取原因，由堆的 (time, 优先级) 定序保证。
- 一次推演可跨多次尝试与多个到期（hb 失败→Waiting→到点 Scheduled→无人领取→sc 等）。

## 关键取舍
- **g' ≥ t0+sc 直接终败，不进入注定超时的 Waiting**：排队时刻已落在总时限上或之外，之后任何 Start/完成都不可能早于总时限，排队只会制造无意义的等待与到期；直接判 Failed(ρ) 使终局与时刻（失败时刻 f）可精确复现，且省一次重试排队。
- **s2s 到期不重试、s2c 到期重试**：s2s 表示活动一直无人领取（调度/容量问题），同一队列条件下重试同样领取不到，只会消耗预算与总时限；s2c 表示已被领取但执行未在时限内完成，是尝试级瞬时问题（机器、负载、抖动），退避后重跑有成功可能，故消耗重试预算。
- **被放弃方案：每类超时都消耗重试预算（含 s2s 与 sc 前的排队）**：该方案会把"无人领取"与"执行超时"混为一谈，并在 g'≥sc 时仍照常 Waiting 再超 sc，产生无信息量的中间状态，终局原因也从 ρ 被污染成 sc。故不采用。

## 操作语义
- 拒绝优先级：参数非法/ErrConfig → ErrClock → 不存在 → ErrStale（k≠当前尝试号）→ ErrTerminal（k 同但已终局）→ ErrState。
- 到期当刻的 Heartbeat/Complete/Fail 先被到期处理超越，旧尝试结果得 ErrStale；同尝试已终局（如第 k 次 s2c 当刻 Complete(k)）得 ErrTerminal。
- now≥时钟 才受理；仅成功的写操作推进时钟。Status 只读，在副本上推演，不改任何状态。
- 终局后状态冻结；尝试号单调递增且 ≤M；progress 随活动携带，跨尝试传递。

## 本地验证
- `go test -race -v ./...`；`go test ./... -fuzz=FuzzCompare -fuzztime=30s`（执行器与逐毫秒朴素模型对照）。
