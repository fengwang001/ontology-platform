# 带背压的流式聚合管线设计

## 1. 链路与包划分

source → parse(Frame) → stage 有界队列 → 聚合(Group) → stage 有界队列 → sink 落盘；
ckpt 负责状态持久化。`stage` 提供有界通道、阻塞计数与历史最大在途数；
管线本体放在 `stage.NewPipe` 中装配，避免再增文件。

- 位置定义：`pos` 为 source 产出的帧序号（从 0 开始单调递增），一帧对应一条记录，
  坏帧也占序号，因此恢复时坏记录计数只需恢复一次、不会被重复统计。
- 帧格式：单行 `key=value`；无 `=` 或 value 非整数即坏帧；`=1` 是合法空键。

## 2. 「已消费位置」推导

设记录 r 生命周期为：source 读出 → 队列 q1 → 聚合 → 队列 q2 → sink 临时文件 →
rename 完成。

- 记 source 位置 P_src：r 可能仍在 q1/q2 或临时文件中，崩溃后从 P_src 重放会
  **丢失全部在途记录**。
- 记 sink 当前落盘位置 P_sink：r 已落盘但 q1/q2 中可能还有 P≤P_sink 的记录，
  从 P_sink 重放会把已落盘记录**重复聚合**（sum 被加倍），输出不再幂等。

正确定义：**检查点位置 C 是「屏障位置 B」，且屏障必须满足：所有 pos<B 的帧都已
穿过 parse、聚合两级队列、sink 临时文件已 rename，并且在途集合为空时才提交 C=B**。

实现上引入 epoch 屏障：

1. 每 E 帧 source 发一个携带 B 的屏障标记。
2. 屏障像数据一样排队通过两级有界队列，因此屏障之前的数据不会与之后的数据乱序。
3. 聚合器在屏障处**冻结当前分组快照**，随屏障交给 sink。
4. sink 收到屏障后先排空本 epoch 的全部 worker（WaitGroup），再把
   `快照 + pos=B + badCount` 写临时文件、fsync、rename；**rename 成功之后**才写检查点。
5. 只有「两级队列中屏障前的数据全部排空、sink rename 完成」这个时刻才提交 C=B。

崩溃恢复时从 C 重放 pos≥C。所有 pos<C 的记录一定已在最终输出里；所有 pos≥C 的
记录一定不在已提交输出里（它们只可能存在于会被清理的临时文件中）。聚合为
`sum(count)`，且输出按 key 排序后逐字节写文件，因此重放产生的输出与未崩溃运行
**逐字节相同**，不丢不重。

## 3. 背压与资源上限

两级通道容量均为 Q；parse 与 sink 各有最多 G 个正在处理的批次/记录；
sink 每 epoch 最多缓冲 E 条。历史最大在途硬上界：

`maxInFlight ≤ 2Q + 2G + E + 2`（+2 为两个屏障标记）。

source 全速、sink 拖慢时，q1/q2 被填满后 source 的发送 select 阻塞，阻塞次数计入
`Blocked`。分组数达到 MaxGroups 时遇到新 key 返回 `ErrTooManyGroups` 并停止整个
管线，不静默丢组。

## 4. 故障与恢复

- sink 输出写 `*.tmp`，成功 rename 为 `out-<pos>`；恢复时先删除全部 `*.tmp`，
  被截断的临时文件永远不可能被当成交付结果。
- ckpt 保留 `ckpt.0`、`ckpt.1` 两个文件交替写；每个文件末尾附 SHA-256 行，
  校验失败或 JSON 损坏即视为无效，回退到另一个完好文件并在 Report 中标记
  `FellBack=true`；两个都坏则从 pos=0 开始。
- source 返回错误：当前 epoch 已落盘部分保留，错误经 Report.Err 如实上报。
- 优雅 Stop：drain 全部在途记录后提交检查点，所有 goroutine 退出；重复 Stop
  幂等（sync.Once）。

## 5. 边界

零记录产出 `out-0`（空内容）；队列容量 1、epoch=1 照常工作；sink 立即报错、
source 立即结束、启动前 Stop 都不泄漏 goroutine。
