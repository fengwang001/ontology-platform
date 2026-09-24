# 带背压的流式聚合管线 — 设计推导

## 1. 拓扑

```
source (Item) → 有界chan → parse → 有界chan → aggregate (map+slow sink) → 文件
```
- 两级有界队列由通用包 `stage` 提供：`Run(ctx, cap, produce, consume)`，
  内部只允许一个 goroutine 执行 `consume`；`Send` 在 chan 满时阻塞并自增阻塞计数；
  包内计数器 `maxInFlight`（= 两级 chan 当前长度之和的历史最大值）通过
  接口断言 `interface{ MaxInFlight() int }` 供测试读取。
- sink 慢：聚合 worker 串行处理，每条记录人为 sleep d。上游 chan 满 →
  parse 的 Send 阻塞 → 第一级 chan 满 → source 阻塞。压力逐级回传。

## 2. 核心推导：检查点的「已消费位置」记到哪

记录 r 带单调 offset。崩溃后从位置 P 重放，要求每条记录恰好生效一次。

- **错误定义 A：P = source 刚读出的位置。**
  此刻 r 可能只在 chan 里、尚未进入聚合 map，更未落盘。重放从 P 之后开始，
  r 既不在内存也不在文件 → **丢失**。
- **错误定义 B：P = sink 已落盘快照覆盖到的位置，而队列非空。**
  设队列里压着 offset ≤ P 的记录，重放会重新聚合它们 → **重复计数**。

关键观察：聚合是 **可交换/可幂等重放** 的（分组内记 min/max/sum/count，
且按「每组最小值」的语义写快照；重放同批数据得到同一结果）。因此不必
逐条去重，只需保证：**被 checkpoint 覆盖的 offset 区间，其聚合结果必须
已经持久化；未被覆盖的区间，必须整体重放。**

正确定义：

> P = 最大的屏障 B，使得所有 offset ≤ B 的记录此刻在途集合为空
> （不在任何 chan、不在 worker 手中），并且它们的聚合结果已包含在
> 一次 temp→rename 完成的输出快照与同一份检查点里。

实现：每 B 条 source 记录注入一个屏障事件（offset=B,2B,…，source 正常
结束时再补一个尾屏障）。屏障随 FIFO 队列流动；聚合 worker 收到屏障时：
(1) 排空保证——FIFO 且单 worker，此前所有 offset ≤ B 的记录必已处理进
map；(2) 写输出快照（temp+fsync+原子 rename+校验和）；(3) 写检查点
（同样 temp+rename，保留最近两份）。三步完成后才应答屏障。

崩溃时刻分析：
- 快照已改名、检查点未改名前崩溃：旧检查点 P 旧 → 区间 (P旧,B] 重放，
  快照覆盖的数据被重新计算，结果幂等相同，不丢不重。
- 检查点写一半：损坏文件校验失败，回退另一份完好文件。
- source 已读但队列非空时崩溃：P 停在上一个已应答屏障，在途记录全部
  重放（它们的 offset > P），不丢不重。

恢复流程：删除输出与检查点的 `*.tmp`（绝不能读回）；校验两个检查点，
取校验通过的较新者，较新损坏则回退较旧者并在 Report 标记 `FellBack`；
从 P 恢复 map，令 source 跳过前 P 条；输出快照按组最小值落盘，重放
逐字节相同（组按 key 排序，JSON 确定编码，文件名固定）。

## 3. 资源约束

- 在途上限 = 两级队列容量之和；`maxInFlight` 每次入队后采样两级 chan
  len 之和。source 全速、sink 每条 sleep 时，断言 maxInFlight ≤ 容量和
  且 source 阻塞次数 > 0（5 万条；sleep 取参数化 d，默认 20µs，约 1s）。
- map 分组数达 `MaxGroups`：返回哨兵错误 `ErrTooManyGroups`，停止摄入，
  已处理部分照常落快照+检查点，错误经 `Wait()` 上报，不静默丢组。

## 4. 记录与故障语义

- 行格式 `key,value`（int64）；key 为空串合法；缺字段、value 非数字、
  空行 → 坏记录，计数 `BadRecords`，不入任何分组。
- source 中途出错：先完成尾屏障落盘与检查点，再把错误如实返回。
- sink 立即出错：管线停止并上报。
- 停止信号（启动前到达同样安全，ctx 预取消）：排空在途、尾屏障落盘、
  worker 退出；`Stop` 幂等（sync.Once）；停止后 goroutine 数回基线。

## 5. 文件格式

- 输出 `out.json`：`{"groups":{"k":{"min":..,"max":..,"sum":..,"n":..}},"offset":P}`，
  先写 `out.json.tmp` 再 rename；尾部附 SHA-256（行 `sha256=<hex>`）。
- 检查点 `ckpt.1/ckpt.2` 轮换写，同样 temp+rename+SHA-256；恢复时
  校验不匹配即视为损坏。逐字节截断的临时文件一律先被 `*.tmp` 清理删除。
