# AES-128 推导与不变量

## 三、w[4..7] 逐步推导（key=00010203…0c0d0e0f，即 w[0..3]=00010203 04050607 08090a0b 0c0d0e0f）

| 步 | 值（十六进制） |
|---|---|
| 1. w[3] | 0c0d0e0f |
| 2. RotWord(w[3])（左循环移 1） | 0d0e0f0c |
| 3. SubWord（Sbox[0d]=d7, [0e]=ab, [0f]=76, [0c]=fe） | d7ab76fe |
| 4. ^Rcon[1]=01（temp[0]^=01） | d6ab76fe |
| 5. w[4]=w[0]^temp=00010203^d6ab76fe | d6aa74fd |
| 6. w[5]=w[1]^w[4]=04050607^d6aa74fd | d2af72fa |
| 7. w[6]=w[2]^w[5]=08090a0b^d2af72fa | daa678f1 |
| 8. w[7]=w[3]^w[6]=0c0d0e0f^daa678f1 | d6ab76fe |

- (甲) 漏 ^Rcon：temp 保持 d7ab76fe，w[4]=00010203^d7ab76fe=**d7aa74fd**（正确 d6aa74fd）。
- (乙) RotWord 反向（右移 1）：0c0d0e0f→0f0c0d0e，SubWord=76fed7ab，^01=77fed7ab，w[4]=00010203^77fed7ab=**77ffd5a8**（正确 d6aa74fd）。
- (丙) 列 db 13 53 45：正确 2·db=(db<<1)^1b=b6^1b=ad、3·13=26^13=35，b0=ad^35^53^45=**8e**；若 ·2 只取低 8 位不归约，2·db=b6，b0=b6^35^53^45=**95**（正确 8e）。

## 二、四条不变量（保证位置 / 钉住它的测试）

1. FIPS-197 向量：`aes.EncryptBlock` 的十轮结构（aes/aes.go），测试 `TestFIPS197Vector`（aes/aes_test.go）。
2. 朴素参照一致：`aes` 导出 SubBytes/ShiftRows/MixColumns/AddRoundKey 可独立复现，`api.NaiveEncryptBlock` 逐轮组合（api/api.go），测试 `TestNaiveReferenceMatch`。
3. 轮密钥缓存：仅 `aes.NewCipher` 调 `ExpandKey`，`EncryptBlock` 零重算（非导出计数器 `expWords`，aes/aes.go），测试 `TestRoundKeyCacheZeroReexpand`。
4. 失败不留痕：所有长度/初始化校验先于任何写入，错误路径零状态变更（api/api.go、aes/aes.go），测试 `TestFailureLeavesNoTrace`（api/api_test.go）。
