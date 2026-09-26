# NOTES — SHA-256 调度推导与不变量索引

输入块：64 字节 `0x01..0x40`；`W[t]=σ1(W[t-2])+W[t-7]+σ0(W[t-15])+W[t-16] (mod 2^32)`。

| t | σ0(W[t-15]) | σ1(W[t-2]) | W[t-7] | W[t-16] | W[t] |
|---|---|---|---|---|---|
| 16 | 9168cdae | 5af7d534 | 25262728 | 01020304 | 1288cd0e |
| 17 | 9bf05735 | d84756b7 | 292a2b2c | 05060708 | a267e020 |
| 18 | a27fdebf | 7f226926 | 2d2e2f30 | 090a0b0c | 57da8221 |
| 19 | aec56200 | 0c3cdc87 | 31323334 | 0d0e0f10 | f94280cb |
| 20 | b74eeb88 | 114177b6 | 35363738 | 11121314 | 0ed8ad8a |
| 21 | bdd67113 | 1042d329 | 393a3b3c | 15161718 | 1c699690 |
| 22 | c451f89d | 4377f09c | 3d3e3f40 | 191a1b1c | 5e224395 |
| 23 | c4af086a | f99d17dc | 1288cd0e | 1d1e1f20 | edf30c74 |

- **(甲)** W[17] 正确值 `a267e020`；σ0 第三项误写成 ROTR^3 时错成 `2267e020`。
- **(乙)** W[16] 正确值 `1288cd0e`；σ1 第三项误写成 ROTR^10 时错成 `4d88cd0e`。
- **(丙)** T1=`f479f06c`、T2=`08909ae5`，正确 a=`fd0a8b51`；Ch/Maj 对调后 a=`fc83eb22`。

## 四条不变量的保证位置与钉住测试

1. **一次性 == 分批喂入**：`sha/sha.go` 的 `(*Hasher).Write`/`Finalize` 对任何切分都走同一个 `compressBlock` 与同一 `state`；`TestChunkEquivalence`（api）逐字节/随机切分对比，`TestSelfCheck` 内置核验。
2. **abc 已知向量**：压缩与大端拼接在 `sha/sha.go` `compressBlock`+`(*Hasher).Finalize`；`TestKnownVectors` 钉住 abc/空串/01..40 三向量与 1..32 截断，`TestSelfCheck` 内置 abc。
3. **Write(A);Write(B)==Write(A‖B)**：`(*Hasher).Write` 只按 64 字节边界压缩、`total` 只累加字节数，跨 Write 缓冲在 `buf`；`TestChunkEquivalence` 全切分点循环钉住。
4. **失败不留痕**：`api/api.go` `New` 先校验 size；`sha/sha.go` `(*Hasher).Write` 在改动任何字段前先判定已定稿/位长溢出，`Finalize` 不修改流式状态；`TestSentinelErrors`、`TestRejectedStateUntouched`（api）与 `TestOverflowNoTrace`（sha 内部）钉住。

非导出计数器 `lastBlocks`（最近一次 Write 实际压缩块数）仅同包测试可读：`TestLastBlocksCounter` 钉住 m∈{100,500,1000,5000,10000} 整块后再写 1 字节压缩块数为 0。
