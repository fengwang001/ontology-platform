# 带背压的流式聚合管线 — 设计推导

模块 `ontology`，仅标准库，状态在进程内存。链路：source → parse → 分组聚合 → sink 落盘。

## 1. 「已消费位置」记到哪

记 source 位置：记录已出 source、压在有界队列中时崩溃，重放从更后面开始，**在途记录丢失**。
记 sink 位置而无其他条件：若 checkpoint 只在「某条落盘」瞬间写下该位置，
而队列里仍压着未完全落盘的记录，恢复区间与已落盘前缀交叠，**重复处理**。

结论：位置定义为 **安全位点（watermark）**——到该位点为止（含）的全部记录，
在恢复时刻必然同时满足：① 已被 sink 完整原子落盘；② 各级在途集合为空。

推进方式（FIFO + sink 顺序提交）：聚合方周期性插入屏障（barrier）。
屏障携带 source 位点，随记录流过 parse→聚合→sink。sink 顺序提交，
当「屏障前所有输出已 fsync+原子改名」且屏障之前无在途记录时，
管线停止从 source 读取，原子写出 checkpoint（位点+各组全量中间聚合快照）。
此前 source 必须暂停推进，因此该位点之后的记录不会漏读；
位点前的全部效果都已落在两份持久文件里，恢复重放区间为 (w0, now]，
与历史前缀不重叠：**不丢不重**。

崩溃窗口只剩两类，均安全：写 checkpoint 期间崩溃 → 文件含 CRC，损坏则回退前一份；
sink 写临时文件期间崩溃 → 未改名，恢复时扫描并清理临时文件，正式输出仍停在上一位点。

## 2. 背压与资源约束

三级有界队列（stage 包，泛型 `Queue[T]`，缓冲 channel + 阻塞 Send）：
source→parse、parse→聚合、聚合→sink，容量 C。下游慢则 Send 阻塞，
压力逐级传回 source；`Queue.MaxInFlight()` 给历史最大在途数，
断言三级之和 ≤ ΣC。feeder 的阻塞 `select-default` 计数 >0 即证明 source 真被阻塞。
内存分组数超硬上限：聚合器返回哨兵错误 `ErrTooManyGroups`，管线停止并上报，不丢组。

## 3. 各包

- source：接口 `Source`：`Next() (rec []byte, pos int64, err error)`、`ReplayFrom(pos)`、`Close()`。
  内存版可配置产出速率（间隔）、总条数、错误位点/错误值；记录格式 `key|value`（一行一帧）。
- parse：`Parse([]byte) (Record, error)`：空行→坏；缺失分隔符或键缺失→坏；空键合法，value 为整数。
  坏记录只递增坏计数，不入组。
- stage：`Queue[T]` 有界、阻塞、`MaxInFlight/Len`；`Stage` worker 骨架；`Coordinator`
  提供 ctx 取消、WaitGroup 与幂等 Stop（sync.Once），保证不泄漏 goroutine。
- sink：`Sink` 输出格式为排序后的 `key=sum\n` 文本；写 `out.tmp.<n>` + CRC 尾行，
  fsync 后原子 rename；构造时扫描清理残留临时文件；`Valid()` 用 CRC 判别完整输出。
- ckpt：两个槽位 `ckpt.a/ckpt.b` 交替写（临时文件+rename+CRC），损坏时回退另一槽，
  状态报告含 `FellBack bool` 与位点、分组 map。

## 4. 崩溃与恢复（进程内模拟：丢弃内存状态，不写未完成的持久文件）

三个崩溃点：A source 读完、某队列非空；B 聚合中途（某批输出已提交、下一批进行中）；
C sink 临时文件写完前（绝不 rename）。恢复流程：清临时文件 → 载入最新完好 checkpoint →
source `ReplayFrom(w)` → 重建内存聚合 → 继续落盘。聚合满足交换/结合（求和）、
输出按键排序，故恢复后输出与一次跑成**逐字节相同**。

## 5. 优雅停止与边界

ctx 取消后各 worker 排空在途记录后退出；重复 Stop 幂等；测试用
`runtime.NumGoroutine` 停止前后比对回到基线。边界：零记录、单记录、同组、
队列容量 1、sink 立即错、source 立即结束、启动前收到停止、空键、缺失键计坏。
