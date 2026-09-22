# DESIGN — 可崩溃恢复的外部排序管线

## 目标与分解

把超过内存预算的记录流排好序输出，允许中途崩溃并从磁盘恢复。
包划分：`record`（定义与编解码）、`spill`（run 文件读写与校验）、
`budget`（驻留字节记账）、`merge`（K 路归并）、`pipeline`（状态机与
检查点）、`cmd/demo`（演示）。

## 记录与 run 文件格式

记录 `Record{Key string, Value []byte, Seq uint64}`，payload 编码：
`uvarint(Seq) | uvarint(len(Key)) | Key | uvarint(len(Value)) | Value`。

run 文件（自描述）：

- 头（14B）：magic `"OSRT"`(4B) | version uint16LE(2B) | count uint64LE(8B)
- 每条记录：payloadLen uint32LE(4B) | payload | CRC32-IEEE(payload)(4B)

截断分类（`spill.Recover` 按读到的位置判定）：

- 头不足 14B → `ErrHeaderIncomplete`
- 长度前缀不足 4B → `ErrLengthPrefixIncomplete`
- 记录体（payload+CRC）不足 → `ErrRecordBodyIncomplete`
- CRC 校验不符 → `ErrCRCMismatch`

恢复语义：返回最大可恢复前缀——已完整且 CRC 正确的记录一条不丢，
半截记录一条不要。头中 count=0 的「零记录 run」合法；头声明 count>0
但后续不足的「空 run 文件」按上述分类报错，二者可区分。

## 等键顺序：比较键推导

要求：相同键的记录按到达顺序输出，且跨 run 成立。

1. run 内排序必须让归并可行，故 run 内按 Key 升序；等键时需一个
   tie-breaker 使 run 内全序。
2. 若用「run 内局部序号」做 tie-breaker：每个 run 的局部序号都从 0
   开始，归并时无法区分 run A 的第 0 条与 run B 的第 0 条谁先到达，
   后到的记录会被排到前面 —— 局部序号只在本 run 内单调，跨 run 无意义。
3. 正确做法：在 Ingest 时由单一计数器为每条记录赋**全局到达序号
   Seq**（并发下由互斥锁保证唯一且单调）。Seq 在所有 run 间全局单调，
   因此 `(Key, Seq)` 是全局全序，且等键时 Seq 序即到达序。
4. run 内排序与 K 路归并使用同一比较键 `(Key, Seq)`，归并的稳定性
   不再依赖 run 编号，等键跨 run 的到达顺序自然成立。

## 内存预算与溢写触发

`budget` 是容量为 L 的记账器：`Acquire(n)` 在已用+n 超过 L 时阻塞
（而非超支），`Release(n)` 归还。Ingest 先把记录编码长度计入预算再
入缓冲；缓冲字节数达到阈值即切出整块交给后台溢写 worker，**溢写
文件落盘后才 Release 对应字节**。因此任意时刻「当前缓冲 + 在途溢写
缓冲」的记账和 ≤ L，驻留上界由记账器硬保证；溢写触发是预算耗尽的
必然结果，而不是可选优化。单条记录编码后超过 L 直接返回
`ErrRecordTooLarge`，不会死循环。`budget` 记录历史最大驻留字节，
供测试断言上界。

## 归并比较次数上界

`container/heap` 维护 ≤K 个 run 的当前头记录。每条输出触发一次
Pop+Push，堆高 ⌈log2(K+1)⌉，每次下沉每层常数次比较，故总比较次数
≤ 4·N·⌈log2(K+1)⌉，远优于「每轮扫描全部 run」的 O(N·K)。
比较次数由非导出计数器记录，测试断言上界。

## 状态机与崩溃恢复

阶段：`Ingest → Spill → Merge → Finalize → Done`。
检查点 `checkpoint.json`（临时文件 + rename 原子替换）记录当前阶段、
run 文件列表、已 Ingest 计数。每个阶段幂等：

- Spill：run 文件写完（含 CRC）才加入检查点列表。
- Merge：输出写 `output.tmp`，不写检查点即崩溃则恢复时整体重写。
- Finalize：`output.tmp` rename 为 `output.run` 后更新检查点为 Done。

恢复：打开目录读检查点，从记录的阶段继续；截断/损坏的 run 由
`spill.Recover` 给出可判定错误并取最大可恢复前缀。测试注入接口：
`spill.WriteRunTruncated`（任意字节截断）与
`Pipeline.SetCrashPoint(stage)`（阶段边界模拟崩溃）。

## 并发

Ingest 可被多协程调用：互斥锁保护缓冲与 Seq 分配，预算 Acquire 阻塞
等待后台溢写释放额度。Close 与 Ingest 竞争：Close 先置关闭标志，
之后的 Ingest 返回 `ErrClosed`，不 panic、不静默丢弃。
