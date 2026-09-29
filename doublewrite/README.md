# 双写缓冲批量页刷写与撕裂页修复（doublewrite）

`doublewrite` 包在一块“按扇区原子写”的磁盘上实现批量页的原子刷写：
同一批脏页在恢复后**要么全是新版本、要么全是旧版本**，任何页都不会出现
新旧内容混合（撕裂页）。

## 磁盘布局与页格式

- 扇区大小固定 `SectorSize = 512` 字节，页大小是扇区的整数倍（测试用 4 扇区页）。
- 磁盘依次为：**原位数据区 → 双写区 → 完成标记扇区**。
- 每页页头（小端）：`magic / pageID / version / payloadLen / crc32c`，负载尾部补零；
  CRC 覆盖除 CRC 字段外的整页字节，因此“只落盘前若干扇区”的撕裂页和静默损坏
  都能被检出。
- 完成标记为一个独立扇区：`magic / batchSeq / pageCount / crc32c`。

## 写入顺序（提交点）

`FlushBatch` 在整批校验全部通过后，严格按以下顺序落盘：

1. `erase-marker`：用零扇区覆盖旧完成标记，使上一批残留失效；
2. `doublewrite`：按页号升序把**整批**新页写入双写区；
3. `marker`：写入完成标记 —— 这是唯一的**提交点**；
4. `inplace`：按页号升序逐页写回原位。

完成标记只有在其全部扇区落盘且 CRC 校验通过时才“有效”，它证明整批新页
已完整、安全地落在双写区。掉电只会让正在写的那个扇区落下前若干字节。

## 恢复判定

`Recover` 读取完成标记：

- **标记无效**（magic/CRC 不符、或标记前掉电）：双写区**整体忽略**，原位一字节不动。
- **标记有效**：逐页比较双写副本与原位：
  - 副本版本 `>` 原位版本 → **前滚**：用双写版本覆盖原位
    （原位 CRC 失败时版本按 **0** 计，因此损坏页也会被有效副本修复）；
  - 原位版本 `>=` 副本版本 → **不动**：双写区是更旧的残留副本，**绝不回滚**；
  - 原位有效且不在本批 → 保持不动。
- 副本自身 CRC 失败或页号越界，则该副本**不可用**，绝不猜测其内容。
- **不可修复条件**：原位 CRC 失败（或页号不符）且没有任何可用副本
  （标记无效、或该页槽副本损坏）。该页进入 `RecoverReport.Unrecoverable`，
  版本按 0 计，磁盘内容不被改动。
- 恢复**幂等**：标记始终保留在盘上，第二次 `Recover` 的判定全部落到“已一致”
  分支，不写任何字节。

## 整批拒绝（不写任何扇区）

以下情况在写第一个扇区之前整体拒绝，错误可 `errors.Is` 区分：

- `ErrDuplicatePageID`：同批页号重复；
- `ErrPageIDOutOfRange`：页号越界；
- `ErrBatchTooLarge`：批大小超过双写区容量；
- `ErrVersionNotGreater`：新版本不大于原位版本（原位损坏按 0 计）；
- 另有 `ErrPayloadTooLarge`、`ErrEmptyBatch`、`ErrBadConfig`。

## 并发与确定性

- 读（`ReadPage`）与刷写（`FlushBatch`）可并发调用；所有批次经同一把互斥锁
  **串行生效**，读者只可能读到某个 CRC 完整的版本。
- 写入按页号升序、固定阶段顺序，因此同一刷写序列 + 同一掉电点
  （`ArmCrash(crashAfter, partialBytes)`）产生**逐字节相同**的结果。
- `SectorDisk.CorruptByte` 对单个字节静默翻转，用于制造不可修复场景。

## 本地验证

```bash
# 全量测试（含竞态检测）
go test -race ./doublewrite/ -count=1 -v

# 全量构建与静态检查
go build ./...
go vet ./...

# 仅看可运行示例（打印输入/输出/恢复判定日志见其它用例的 bytes.Buffer）
go test ./doublewrite/ -run Example -v
```

`TestAllCrashPoints` 遍历双写区每个扇区、完成标记前后、原位每页每个扇区的
掉电点，并对每个扇区取 `partial ∈ {0,1,255,511}` 四种撕裂前缀，断言批内页
同新或同旧；`TestStaleCopyNoRollback`、`TestSilentCorruptionUnrecoverable`、
`TestRecoverIdempotentBytewise`、`TestDeterministicSameCrashPoint`、
`TestConcurrentReadersAndFlushes` 分别覆盖其余保证。
