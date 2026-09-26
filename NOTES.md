# CRC-32 推导与不变量

## 第三节：首字节 '1'(0x31) 的八次逐位循环（初值 0xFFFFFFFF，crc ^= 0x31 → 0xFFFFFFCE）

| 位 | 该位 | 处理后 crc |
|---|---|---|
| 0 | 0 | 0x7FFFFFE7 |
| 1 | 1 | 0xD2477CD3 |
| 2 | 1 | 0x849B3D49 |
| 3 | 1 | 0xAFF51D84 |
| 4 | 0 | 0x57FA8EC2 |
| 5 | 0 | 0x2BFD4761 |
| 6 | 1 | 0xF8462090 |
| 7 | 0 | 0x7C231048 |

- (甲) MSB-first 非反射（poly 0x04C11DB7）：错成 **0xFC891918**（正确值 0xCBF43926）
- (乙) 查表索引漏 `^ 当前字节`（写成 `crc & 0xFF`）：错成 **0xE60914AE**（等价于喂了 9 个 0x00）
- (丙) 漏掉最后的 `^ xorout`：错成 **0x340BC6D9**

## 四条不变量：保证位置 + 钉住它的测试

1. 表驱动 == 逐位参照：`crc/crc.go` 中 `table` 由同一逐位循环生成、`UpdateTable`/`UpdateBitwise` 共用该 poly；测试 `TestTableMatchesBitwise`（crc/crc_test.go）
2. 已知向量 0xCBF43926：`api/api.go` 的 `SelfCheck` 内置核验；测试 `TestKnownVector`（api/api_test.go）
3. 拼接性质：`api.Update` 只在 `crc.Digest` 上追加字节、状态机不重置；测试 `TestConcatenation`（api/api_test.go）
4. 失败不留痕：`api.New`/`Verify` 的拒绝路径只读不写（先判错再返回，无状态变更）；测试 `TestFailureLeavesNoTrace`（api/api_test.go）
