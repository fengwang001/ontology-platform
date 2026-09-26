# Merkle 哈希树：推导与不变量

## 第三节推导（fnv32 / leafHash=fnv32(0x00‖d) / combine=fnv32(0x01‖l‖r)，大端）

八叶分步表（数据 "1".."8"，leafHash 为十六进制）：

| i | data | leafHash   | i | data | leafHash   |
|---|------|------------|---|------|------------|
| 0 | "1"  | 0x2076AF6A | 4 | "5"  | 0x1C76A91E |
| 1 | "2"  | 0x1F76ADD7 | 5 | "6"  | 0x1B76A78B |
| 2 | "3"  | 0x1E76AC44 | 6 | "7"  | 0x1A76A5F8 |
| 3 | "4"  | 0x1D76AAB1 | 7 | "8"  | 0x1976A465 |

- 第 1 层：0xA6ABC1F2, 0x2FEBAB2E, 0x588BA87E, 0x4F373FD5
- 第 2 层：0xCD0D31A7, 0x097FB84D；根哈希：**0x0887B3C1**
- (甲) 叶 2("3") 路径三兄弟 = [0x1D76AAB1, 0xA6ABC1F2, 0x097FB84D]（L0[3]、L1[0]、L2[1]）；Verify(2,"3",路径) 还原出 0x0887B3C1，等于根，**能**。
- (乙) combine 参数写反：L1[0] 错成 **0xE0DDFA82**（正确 0xA6ABC1F2）；根错成 **0x8C22EE5E**（正确 0x0887B3C1）。
- (丙) Verify 左右判定写反：还原根错成 **0x23E1A70F**（正确 0x0887B3C1）。

## 四条不变量：保证位置 + 钉住它的测试

1. 路径可验证：`tree.Proof` 逐层取 `i^1` 兄弟、`tree.Verify` 用同一位规则 `(index>>k)&1` 还原 → `TestProofVerifyRoundTrip`
2. 与朴素参照一致：`tree.Build` 自叶向上逐层 `Combine`，测试内 naiveRoot 独立重算对照 → `TestRootMatchesNaive`
3. 防篡改：`fnv.LeafHash` 带 0x00 域分隔且逐字节混入，任一字节改动即改根 → `TestTamperDetected`
4. 失败不留痕：`Build/Proof/Verify` 先校验（哨兵错误）后动作，树构建后不可变，拒绝路径零写入 → `TestRejectionKeepsState`
