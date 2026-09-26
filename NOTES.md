# AES-128 推导与不变量

密钥 `000102030405060708090a0b0c0d0e0f`，w[0..3]=`00010203 04050607 08090a0b 0c0d0e0f`。

| # | 步骤 | 值 |
|---|---|---|
| 1 | w[3] | `0c0d0e0f` |
| 2 | RotWord（左循环移 1 字节） | `0d0e0f0c` |
| 3 | SubWord（逐字节查 Sbox） | `d7ab76fe` |
| 4 | ^Rcon[1]=`01000000` | `d6ab76fe` |
| 5 | w[4]=w[0]^(4) | `d6aa74fd` |
| 6 | w[5]=w[1]^w[4] | `d2af72fa` |
| 7 | w[6]=w[2]^w[5] | `daa678f1` |
| 8 | w[7]=w[3]^w[6] | `d6ab76fe` |

- (甲) 漏掉 `^Rcon`（或误用 Rcon[0]=0）：w[4]=w[0]^`d7ab76fe` 错成 **`d7aa74fd`**（正确 `d6aa74fd`）。
- (乙) RotWord 方向反（右循环移 1）：`0f0c0d0e`→SubWord `76fed7ab`→^`01000000` `77fed7ab`→^w[0]，w[4] 错成 **`77ffd5a8`**（正确 `d6aa74fd`）。
- (丙) `·2` 用普通整数乘不取模：列 `db 13 53 45`，b0=`b6`^(3·13=`35`)^`53`^`45` 错成 **`95`**（正确 `ad`^`35`^`53`^`45`=`8e`）。

不变量 → 代码位置 / 钉住的测试：

1. FIPS-197 向量：`aes/aes.go` 的 `EncryptBlock`（AddRoundKey+9 轮+无 MixColumns 末轮）；`TestFIPS197Vector`。
2. 与朴素参照一致：`aes/aes.go` 的 `NaiveEncrypt`（独立 `naiveMixColumns` 走 `gf256.Mul`）；`TestNaiveReference`，并发侧 `TestConcurrentEncrypt`。
3. 轮密钥缓存：`aes/aes.go` 的 `New` 只调用一次 `expandKey`，`EncryptBlock` 只读 `rk` 且把非导出计数器置 0；`TestRoundKeyCacheCounter`（m=100/1000/10000）。
4. 失败不留痕：`api/cipher.go` 三类哨兵错误（`ErrInvalidKey`/`ErrInvalidBlock`/`ErrUninitialized`）的校验全部先于任何写入，输入先 copy 再处理；`TestRejectedOperationsLeaveNoTrace`。
