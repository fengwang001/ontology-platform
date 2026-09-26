# DH 密钥交换 NOTES

## 推导：modpow(2, 90, 29)，90 = 0b1011010（平方-乘，高位→低位，每行：先平方，该位为 1 再乘 base=2）

| 步 | 当前位 | 平方后 r | 乘 base 后 r（该位最终结果） |
|---|---|---|---|
| 1 | 1 | 1 | 2 |
| 2 | 0 | 4 | 4 |
| 3 | 1 | 16 | 3 |
| 4 | 1 | 9 | 18 |
| 5 | 0 | 5 | 5 |
| 6 | 1 | 25 | 21 |
| 7 | 0 | 6 | 6 |

modpow(2,90,29) = **6**（验算：2^28≡1 (mod 29)，2^90 = 2^(28·3+6) ≡ 2^6 = 64 ≡ 6）。

- **(甲)** 若每位都乘 base：r 依次 2, 8, 12, 27, 8, 12, **27**，错成 27（此时指数为 2^7−1=127，2^127≡2^15≡27）；正确值是 6。
- **(乙)** A = 2^5 = 32 ≡ **3**，B = 2^11 = 2048 ≡ **18**，S = 3^11 = 18^5 ≡ **15**。若不校验公钥，恶意 B=1 → S = 1^5 = **1**：共享密钥被钉成与私钥无关的公开常量，攻击者无需解离散对数即知密钥，机密性完全丧失（小阶子群/单位元攻击）。
- **(丙)** 错算 A·B mod p：Alice 得 3·18 = 54 ≡ **25**，Bob 得 18·3 ≡ **25**，二者**仍相等**（乘法可交换），但等于 g^(a+b) 而非 g^(ab)=15；窃听者用公开的 A、B 直接相乘即得同一值，秘密性完全丧失。

## 四条不变量：保证位置与钉住它的测试

1. **交换一致**：`dh.Group.Secret`/`PublicKey` 同为 modpow，(g^b)^a = g^(ab)（`dh/dh.go`）；测试 `TestExchangeConsistency`（api/api_test.go）。
2. **与朴素参照一致**：`modarith.Modpow` 平方-乘（`modarith/modarith.go`）；测试 `TestModpowNaive`（modarith/modarith_test.go）、`TestNaiveConsistency`（api/api_test.go）。
3. **快速幂乘法次数不随 e 线性增长**：`modarith` 非导出计数器 `lastMulCount`（`modarith/modarith.go`，不出现在公开接口）；测试 `TestModpowMulCountSublinear`（同包白盒读取，modarith/modarith_test.go）。
4. **失败不留痕**：`dh` 先校验后计算、`Group` 构造后不可变、哨兵错误三者互异（`dh/dh.go`）；测试 `TestRejections`（api/api_test.go）。
