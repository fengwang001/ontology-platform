# DESIGN：带背压的流式聚合管线

范围：source → parse → 分组聚合 → 落盘输出。仅标准库，状态在进程内存与本地文件。

## 1. 背压下「已消费位置」的推导

记 source 位置 P（已读出原始字节条数），sink 位置 D（已原子落盘条数）。

- 若 checkpoint = P：崩溃时队列里在途（已读出未落盘）的记录在恢复时被跳过 → **丢数据**。
- 若 checkpoint = D 且不管在途：恢复时从 D 重放，但 D 之后已有数据落盘时会再处理一遍 → **重复**。

关键观察：每条记录在流水线中经历三个阶段 S0（source 出队）→S1（聚合出队）→S2（sink
改名成功）。引入周期性 **barrier（屏障事件）**：source 每 N 条发一个带序号 b 的 barrier，
它排在普通事件之后，逐站向前传。当 sink 对 barrier b 完成「临时文件写完 → 原子改名」
后，管线中序号 ≤ b 的全部事件都已 S2（FIFO 保证在途为空），此时才写 checkpoint：

- Pos = barrier b 覆盖的最后一条源记录序号（**sink 已确认落盘的位置**）；
- Groups = 此刻各分组的中间聚合快照；Bad = 坏记录累计数。

这就是正确位置：它既是「落盘已确认」的位置，又只在**各阶段在途集合为空（对 ≤b 而言）**
时推进。恢复时 source 从 Pos+1 重放、聚合从 Groups 快照续算，故不丢（在途的被重放）不重
（已落盘的不会再落盘：恢复输出按分组求和，checkpoint 之后的段与之前的段不重叠）。

崩溃三时刻（均由测试钩子确定性制造）：
1. source 已读完但队列非空；2. 聚合处理中途；3. sink 写完临时文件但未改名。
前两者：最新 checkpoint 之前的段已落盘，之后全部重放。第三种：临时文件在恢复启动时被
识别（`*.tmp` 且缺少合法校验和尾）并删除，该段从 checkpoint 重放。

## 2. 阶段骨架与资源约束

- 每阶段一条有界 channel（容量 C）+ 单 worker，事件为 `{序号, 记录, barrier}`。
- 在途数 maxInflight = 两个 channel 的 `len()` 之和（容量边界：任何时刻每级 ≤C），
  硬上界 2C（= 各级容量之和），非导出 `maxInflight` 记录历史最大值。
- 上游发送遇满 channel 即阻塞，`blocked`（非导出）用 select default 探测并计数。
- sink 慢（每条 1ms）必然反压到 source：断言 maxInflight ≤ 2C 且 blocked>0。
- 分组数超 MaxGroups：返回哨兵错误 ErrTooManyGroups 并优雅停止，不静默丢组。

## 3. sink 与临时文件

输出行 `key<TAB>sum\n`，末行 `crc32=<十六进制>\n`（覆盖此前全部字节）。先写
`out.tmp`，sync 后原子 rename 为 `out`。恢复时删除任何残留 `*.tmp`；对 tmp 逐字节
截断时，缺合法尾或校验和不符 → 一律判为未完成临时文件，绝不当成有效输出。

## 4. checkpoint

`ckpt.1`/`ckpt.2` 两个文件轮转（保留最近两个），内容为 JSON + crc32 尾。加载时优先读
较新者，校验和不符则回退另一个，并置 FellBack=true。写同样走 tmp+rename。

## 5. 并发与优雅停止

Pipeline 用 ctx+WaitGroup 管理三 worker；Stop 幂等（sync.Once），先停 source 再排空
下游或取消。source 报错：已处理段落盘+写 checkpoint，错误经 Run 返回。测试在 Stop
前后用 runtime.NumGoroutine 比对回到基线。

## 6. 包划分

source（可注入数据源）/ parse（解析，坏记录计数）/ stage（通用阶段骨架）/ sink（原子
落盘+校验和）/ ckpt（检查点轮转恢复）/ pipeline（接线编排）/ cmd/demo（判定演示）。
