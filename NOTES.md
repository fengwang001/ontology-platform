# CRC-32 查表校验：推导与不变量

## 第三节推导：init=0xFFFFFFFF，首字节 '1'(0x31)，crc ^= 0x31 → 0xFFFFFFCE

| 位 | LSB | 处理后 crc |
|---|---|---|
| 0 | 0 | 0x7FFFFFE7 |
| 1 | 1 | 0xD2477CD3 |
| 2 | 1 | 0x849B3D49 |
| 3 | 1 | 0xAFF51D84 |
| 4 | 0 | 0x57FA8EC2 |
| 5 | 0 | 0x2BFD4761 |
| 6 | 1 | 0xF8462090 |
| 7 | 0 | 0x7C231048 |

三问（正确值均为 0xCBF43926）：

- (甲) MSB-first 非反射（poly=0x04C11DB7）→ 错成 **0xFC891918**（反射方向相反，等价于 CRC-32/BZIP2）。
- (乙) 查表索引漏 `^ b` 写成 `crc & 0xFF` → 错成 **0xE60914AE**（结果只与长度有关，与内容无关）。
- (丙) 漏掉最终 `^ xorout` → 错成 **0x340BC6D9**（恰为正确值的按位取反）。

## 四条不变量：保证位置 + 钉住它的测试

1. 表驱动==逐位参照：`crc/crc.go` 的 `New` 用逐位循环建表、`UpdateTable` 用 `table[(crc^b)&0xFF]^(crc>>8)`；测试 `TestTableMatchesBitwise`（crc/crc_test.go）。
2. 已知向量 0xCBF43926：`crc/crc.go` 常量 `Init`/`XorOut` 与表生成；测试 `TestKnownVector`（crc/crc_test.go、api/api_test.go 各一份）。
3. 拼接性质：`api/api.go` 的 `Update` 在同一实例上顺序流式累计、不重置 `crc`；测试 `TestConcatProperty`（api/api_test.go）。
4. 失败不留痕：`api/api.go` 的 `New` 先校验 poly 再建实例、`Verify` 只读不写并返回哨兵错误；测试 `TestRejectedOpsKeepState`（api/api_test.go）。
