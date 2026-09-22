# DESIGN - 可崩溃恢复的外部排序管线

模块 `ontology`。包划分：`record`（记录与编解码）、`spill`（run 文件格式与校验）、
`budget`（内存记账）、`merge`（K 路堆归并）、`pipeline`（状态机与检查点）、`cmd/demo`。

## 1. 记录与 run 文件格式

记录 `Record{Key string, Value []byte, Seq uint64}`，编码为
`keyLen u32 | valLen u32 | seq u64 | key | value`，记账大小 `Size() = 16+len(Key)+len(Value)`。

run 文件 = 自描述头 + 记录序列：

- 头（20B）：magic 4B `OSR1` | version u16 | reserved u16 | count u64 | headerCRC u32（前 16B 的 CRC32）。
- 每条记录：`payloadLen u32 | payload | CRC32(payload) u32`。

头里带 count 使「只有头、count=0 的合法空 run」与「0 字节空文件 / 声称有记录却被截断的文件」
可区分：前者读出 0 条记录且无错，后者报可判定错误。

## 2. 等键顺序：比较键推导

**要求**：相同键的记录按到达顺序输出，且跨 run 成立。

**为什么 run 内序号不够**：设记录按到达次序被切进不同 run。若比较键用「run 内局部序号」，
则每个 run 的局部序号都从 0 重新开始。例：记录 a（到达第 1，run1 局部序号 5）与记录 b
（到达第 2，run2 局部序号 0）。归并时按 (key, 局部序号) 比较，b 的 0 < a 的 5，b 排在 a 前
——后到的记录被排到前面，违反到达顺序。根本原因是局部序号的编号体系随 run 重置，
不同 run 的序号不在同一全序上，不可比。

**推导**：要让跨 run 的等键记录可比，序号的赋值必须在任何切分发生之前、且全局唯一单调。
Ingest 是唯一的到达入口，因此在 Ingest 临界区内分配**全局到达序号 Seq**（单调递增计数器），
它与「记录被切进哪个 run、何时溢写」完全解耦：溢写只是搬运，不改变 Seq。
于是任意两条等键记录 x、y，x 先到达 当且仅当 x.Seq < y.Seq，与所在 run 无关。

**结论**：比较键为 `(Key, Seq)` 的字典序。run 内按该键排序，归并也按该键比较；
因 Seq 全局唯一，该键是全序，归并结果确定且与到达顺序一致。并发 Ingest 时，
「到达顺序」定义为获取 Ingest 锁的串行化顺序，Seq 在同一临界区分配，定义自洽。

## 3. 内存预算与溢写触发的关系

`budget` 是硬上限记账器：`TryAcquire(n)` 仅当 `used+n <= limit` 才成功，否则返回
`ErrOverBudget`；`max` 记录历史峰值（非导出字段，经 `Max()` 读出）。

`pipeline.Ingest` 的关系如下：

1. 若 `rec.Size() > limit`：该记录永远无法驻留，直接返回可判定错误 `ErrRecordTooLarge`，
   不进入缓冲（否则溢写后仍放不下，会死循环）。
2. 若 `TryAcquire` 失败：先同步溢写当前缓冲（排序、写 run、更新检查点、Release 全部字节），
   再重试；此时缓冲已空，必然成功（由 1 保证单条可放）。
3. 因此任意时刻驻留字节 = 当前缓冲记账值 <= limit，溢写文件数约为 总量/limit。

## 4. 状态机与崩溃恢复

状态：`Ingesting -> Spilled -> Done`。检查点 `checkpoint.json`（写临时文件后 rename，原子替换）
记录状态、run 列表、已接收计数、下一 Seq。

- **Ingest**：缓冲满即溢写并落检查点。此阶段崩溃只丢未溢写的内存缓冲（无 WAL，见 6 节）。
- **Close**：flush 残余缓冲 -> 写检查点 `Spilled`（崩溃点 A）-> 归并各 run 到 `output.tmp`
  （归并中途可崩溃，崩溃点 B，留下半截 `output.tmp`）-> 归并完成后、`rename` 前
  （崩溃点 C）-> `rename output.tmp -> output.run` -> 写检查点 `Done`。
- **恢复**（重新 `Open` 同一目录）：状态为 `Spilled` 时，删除可能存在的半截 `output.tmp`，
  先对每个 run 做「最大可恢复前缀」修复（截断尾部的半截记录），再重新归并、Finalize；
  状态为 `Done` 时直接复用 `output.run`。归并是确定性的（全序比较键），故恢复输出与
  不崩溃时逐字节相同。

## 5. 截断分类（恢复语义）

`spill.RecoverPrefix` 从头扫描，返回已完整记录的前缀与分类错误（哨兵）：

- 头不足 20B 或头 CRC 坏 -> `ErrHeaderIncomplete`
- 读长度前缀时剩余 < 4B（含 0B，因为头里 count 声明还有更多记录）-> `ErrLengthPrefixIncomplete`
- 长度前缀已读、payload 不足 -> `ErrRecordBodyIncomplete`
- payload 完整但 CRC 不足 4B 或校验不符 -> `ErrCRCMismatch`（无法验证完整性即视为校验失败）

半截记录一律丢弃，已完整记录一条不丢。

## 6. 并发与限制

Ingest 可并发：互斥锁保护缓冲、Seq 分配与溢写；`Close` 置 closed 标志后的 Ingest 返回
`ErrClosed`，不 panic、不静默丢弃。已知限制：Ingest 阶段（未溢写缓冲）崩溃会丢该缓冲，
需要 WAL 才能覆盖，本题三个崩溃点均在缓冲为空的状态边界，不受影响。
