# FINDINGS - 故障注入与恢复结论

测试夹具：50 条定长记录（key 4B + value 4B，payload 24B），头 20B，
每条 entry = 4B 长度前缀 + 24B payload + 4B CRC = 32B，文件全长 1620B。
对全部 1619 个截断点（保留 1..1619 字节）逐一断言（`spill.TestTruncateEveryByte`）。

## 1. 截断点分类（字节位置区间 -> 结论）

| 字节位置区间（保留长度 off） | 恢复分类 | 可恢复前缀 |
|---|---|---|
| 1..19 | ErrHeaderIncomplete（头部不完整） | 0 条 |
| 20+32i .. 20+32i+3（i=0..49） | ErrLengthPrefixIncomplete（长度前缀不完整） | i 条 |
| 20+32i+4 .. 20+32i+27 | ErrRecordBodyIncomplete（记录体不完整） | i 条 |
| 20+32i+28 .. 20+32i+31 | ErrCRCMismatch（CRC 不足 4B，无法验证即判校验失败） | i 条 |

结论：全部 1619 个截断点均被正确分类，四类错误都被覆盖；已完整记录一条不丢、
半截记录一条不要。`Repair` 就地截掉损坏尾部并修正头中 count，修复后 `ReadAll` 干净通过
（`spill.TestRepairTruncatedRun`：截在第 10 条记录体中间，恢复出 9 条）。

## 2. 阶段边界崩溃恢复（pipeline）

夹具：500 条记录、预算 2048B（约 15 个 run），先跑出无崩溃参照输出，
再分别在三个崩溃点注入 `ErrCrashInjected` 后重开目录恢复
（`pipeline.TestCrashRecoveryByteIdentical`）。

| 崩溃点 | 崩溃时磁盘状态 | 恢复动作 | 结论 |
|---|---|---|---|
| Spill 完成后 | checkpoint=Spilled，run 齐全，无 output.tmp | 直接重新归并 + Finalize | 输出与参照逐字节相同 |
| Merge 中途（第 6 条输出后） | checkpoint=Spilled，output.tmp 半截 | 丢弃 output.tmp，重新归并 | 输出与参照逐字节相同 |
| Finalize 之前 | checkpoint=Spilled，output.tmp 完整未 rename | 重新归并覆盖 output.tmp，rename | 输出与参照逐字节相同 |

另：并发 Ingest（8 协程 x 5000 条，与 Close 竞争）下成功接收数 == 最终输出数、
序号无重复、最大驻留 <= 上限，Close 后 Ingest 返回 ErrClosed
（`pipeline.TestConcurrentIngestAndClose`，`-race` 干净）。
