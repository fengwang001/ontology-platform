# 设计：带背压的流式聚合管线（进程内状态，仅标准库）

## 1. 记录与位置
source 产出带单调序号 Seq（从 1 起）的原始字节。位置 N 表示「序号 <= N 的记录已被确定处理」。坏记录也算已处理（只不进分组），序号照占，恢复不重放。

## 2. 已消费位置到底记到哪一条（核心推导）
链路：source → Q1(parse) → Q2(agg) → sink。
- 记 source 读取位置：记录可能只在 Q1/Q2 中、尚未聚合落盘，崩溃即丢 → 错。
- 记 sink 落盘位置：Q 中在途记录重放会重复；且内存聚合中间态在崩溃后无法还原 → 错。

正确定义：`cp.Seq` 是「其全部效果（聚合中间态 + 输出）都已稳定」的最大前缀。
检查点时机只能是**静止点**：source 暂停推入，屏障（barrier, Seq=B）流经各阶段；屏障到达 sink 时，Q1、Q2 中 Seq<=B 的在途记录已全部排空，此时把 agg 的聚合快照与 B 一起写出。即 `cp.Seq = 最近一个「各阶段在途集合为空」时刻屏障携带的序号`。静止点前 source 已停推，故不存在更老序号在途。

## 3. 输出语义：幂等 + 快照式
sink 不逐条追加，而是在静止点把聚合快照确定性序列化（键升序）写 tmp、flush、再原子 rename，文件末尾附 CRC32。只有 rename 后的文件有效，残留 tmp 一律清理。恢复时以 `cp.Groups` 重建 agg，重放 `Seq>cp.Seq` 的记录，最终输出与不崩溃逐字节相同。写序：先 rename 输出，再写 cp；崩在两者之间只会重做确定工作，幂等不重不丢。

## 4. 背压
每级 channel 容量 C，Send 在 Q 满时阻塞，阻塞点在 source 协程内，直接传导回 source（`Blocked` 记录阻塞次数）。各级在途 ≤ C，全管线历史最大在途 ≤ ΣC。stage 用非导出 `maxInFlight` 记峰值（进入 +1、离开 −1）。

## 5. 优雅停止 / 崩溃
`Stop` 幂等（sync.Once）：source 停推 → 注入尾屏障 → 各阶段排空退出 → Wait 回收全部协程。启动前 Stop 则 Run 立即返回。
崩溃注入为测试钩子，恢复 = 新建 Pipeline 重新 Run（状态仅在构造体内）。三时刻：a) source 读完且 Q 非空；b) agg 处理中途；c) sink 写完 tmp 未 rename。

## 6. ckpt
两份轮转 cp1/cp2（各附 CRC32）。加载：取校验完好且 Seq 最大者；恰好一坏则回退另一份并置 `FellBack`；皆坏从 0 开始。

## 7. 边界
空输入也产出合法空输出 + cp(0)；容量 1 成立；空键合法；缺 `=` 或 value 非整数为坏记录；组数超上限返回 `ErrTooManyGroups` 并停止；source 错误先排空、写 cp 再上报。

## 8. 包与文件（每个 <200 行，共 9 个 .go）
stage（有界队列/屏障/停止/峰值）、source、parse、agg、sink、ckpt、pipeline（编排）、cmd/demo、pipeline_test（全量表驱动，唯一 _test.go）。
