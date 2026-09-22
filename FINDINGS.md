# FINDINGS — 故障注入与恢复结论

测试文件：`spill/spill_test.go`、`pipeline/pipeline_test.go`。
格式常量：头 14B；每条记录槽 = 4B 长度前缀 + payload + 4B CRC。
测试 run：50 条记录，payload = 10+vlen 字节（vlen 1..11），
记录槽 19..29B，文件总长 1199B，逐字节截断点共 1198 个。

## 逐字节截断分类（50 条记录的 run）

| 截断点区间（字节） | 恢复分类 | 可恢复前缀 |
|---|---|---|
| [1, 13]（头内） | `ErrHeaderIncomplete` | 0 条 |
| 记录 i 槽的前 4B（长度前缀区） | `ErrLengthPrefixIncomplete` | i 条 |
| 记录 i 槽的其余字节（payload+CRC 区） | `ErrRecordBodyIncomplete` | i 条 |
| 恰好落在记录边界（= 记录 i+1 槽起点） | `ErrLengthPrefixIncomplete`（下一条前缀不足） | i 条 |

结论：1198 个截断点全部落入上表且被逐一断言（`TestTruncateEveryByte`）；
已完整记录一条不丢、半截记录一条不要。

## CRC 不匹配类

纯截断不会产生 CRC 不匹配（CRC 在记录尾部，截断先触发长度类错误）。
构造比特翻转验证：首条 payload 翻转 → `ErrCRCMismatch`，前缀 0 条；
末条 CRC 翻转 → `ErrCRCMismatch`，前缀 9/10 条。四类错误均可判定。

## 空 run 与零记录 run

- 零记录 run（头 count=0，仅 14B）：合法，`ReadRun` 返回 0 条无错误。
- 空 run 文件（头声明 count>0 但无记录体）：`ErrLengthPrefixIncomplete`。
- 二者可区分（`TestZeroRecordRunDistinguishable`）。

## 阶段边界崩溃恢复

测试：`TestCrashRecovery`（3000 条记录、预算 4096B、多 run），
三个崩溃点各恢复一次，输出与不崩溃参考运行逐字节相同。

| 崩溃点 | 崩溃时磁盘状态 | 恢复结论 |
|---|---|---|
| Spill 完成后 | 全部 run 已落盘并记入检查点（stage=ingest/spill） | 重进状态机执行 Merge/Finalize，输出逐字节相同 |
| Merge 中途 | `output.tmp` 写了一半，检查点 stage=merge | 恢复时整体重写 `output.tmp`（幂等），输出逐字节相同 |
| Finalize 之前 | `output.tmp` 完整，检查点 stage=finalize | 恢复时 rename 为 `output.osrt`，输出逐字节相同 |

## 其他结论

- 驻留上界：20 万条记录、预算为总量 1/50，run 数 ≥ 40，
  `MaxResident` 从未超过上限（`TestResidentBound`）。
- 比较次数：堆归并实测比较次数 ≤ 4·N·⌈log2(K+1)⌉。
- 并发：8 协程 × 5000 条 Ingest 与后台溢写并行，记录数守恒、
  无重复、上界成立；Close 后 Ingest 返回 `ErrClosed`；`-race` 干净。
- 曾修复的死锁：Ingest 先 Acquire 后换缓冲时，缓冲恰好占满预算
  会永久阻塞；改为预算不足时先换出缓冲再重试（见 pipeline.go）。
