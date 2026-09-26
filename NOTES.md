# NOTES — SHA-256 压缩函数与增量哈希

## 第三节推导：W[16..23] 分步表（消息字节 0x01..0x40，全为十六进制）

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

- (甲) σ0 第三项误为 ROTR^3：σ0(W[2]) 由 9bf05735 错成 1bf05735，W[17] 正确值 a267e020，错值 2267e020。
- (乙) σ1 第三项误为 ROTR^10：σ1(W[14]) 由 5af7d534 错成 95f7d534，W[16] 正确值 1288cd0e，错值 4d88cd0e。
- (丙) 第 0 轮 Ch/Maj 对调：T1 用 Maj(e,f,g)=1b0758af（原 Ch=1f85c98c），T2 用 Ch(a,b,c)=3e67b715（原 Maj=3a6fe667），a 正确值 fd0a8b51，错值 fc83eb22。

## 第二节四条不变量：保证位置与钉住它的测试

1. 一次性 == 增量：`sha.Write` 满 64 字节即压缩、余量留缓冲（sha/sha.go），`api.SelfCheck` 核验；测试 `TestSplitInvariance`、`TestVsStdlib`。
2. 已知向量 abc：`sched.IV`/`sched.K` 常量 + `sha` 压缩与 padding（sha/sha.go）；测试 `TestVectorABC`，自检 `api.SelfCheck`。
3. 分块不变：`sha.Write` 的缓冲拼接与切分点无关（sha/sha.go）；测试 `TestSplitInvariance`（循环覆盖全部切分点）。
4. 失败不留痕：`api.New` 先校验 size 再建对象；`sha.Write` 先查 `done`/`limit` 再改任何字段（sha/sha.go）；测试 `TestRejectedNoTrace`、`TestOverflowNoTrace`、`TestWriteAfterFinalize`。
