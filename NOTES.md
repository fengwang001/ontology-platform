# NOTES — DH 密钥交换推导与不变量

## 第三节：modpow(2, 90, 29)，90 = 0b1011010（平方-乘，高位到低位）

| 步骤 | 位 | r（十进制） |
|---|---|---|
| 1 | 1 | 2 |
| 2 | 0 | 4 |
| 3 | 1 | 3 |
| 4 | 1 | 18 |
| 5 | 0 | 5 |
| 6 | 1 | 21 |
| 7 | 0 | 6 |

结果：2^90 mod 29 = 6（验：2 的阶为 28，90 mod 28 = 6，2^6 = 64 ≡ 6）。

- (甲) 每位都乘 base 相当于指数按 e←2e+1 递推，7 位算出 2^127 mod 29 = 27（127 mod 28 = 15，2^15 ≡ 27），正确值是 6。
- (乙) A = 2^5 mod 29 = 3，B = 2^11 mod 29 = 18，共享 S = 2^55 ≡ 2^27 ≡ 15。若不校验公钥，恶意 B=1 使 S = 1^5 = 1：共享密钥被强制为公开常量，攻击者无需解离散对数即知密钥，机密性完全丧失（小阶子群限制攻击）。
- (丙) 错算成 A·B mod p：Alice 得 3·18 = 54 ≡ 25，Bob 得 18·3 ≡ 25。二者仍相等（都等于 g^(a+b) = 2^16 ≡ 25），但任何看到公钥 A、B 的人都能算出同一值，交换毫无安全性。

## 第二节四条不变量的保证位置与钉住测试

1. 交换一致：`dh.Group.Secret` 与 `PublicKey` 同用 `modarith.ModPow`，S = (g^b)^a = g^(ab)；测试 `api.TestExchangeConsistency`。
2. 与朴素参照一致：`modarith.ModPow` 平方-乘实现；测试 `modarith.TestModPowNaive`（逐值对比循环 e 次的朴素参照）。
3. 快速幂乘法次数非线性：`modarith` 非导出计数器 `lastMulCount`（包级 atomic，不出现在公开接口）；测试 `modarith.TestMulCountSublinear`（e = 2^k−1，k=100…10000，次数 ≤ 2k+常数）。
4. 失败不留痕：`dh` 的三个校验在任何计算前返回哨兵错误，`Group` 创建后无可变状态；测试 `api.TestErrorsDistinct`、`api.TestRejectionLeavesNoTrace`。
