# NOTES — GF(2^8) / 0x11B

## 一、八行分步表（规则：加=XOR；乘=多项式乘后模 0x11B）

| # | 运算 | 关键步骤 | 结果 |
|---|---|---|---|
| 1 | Mul(0x57,0x83) | 俄式乘：0x57⊕0xAE⊕(0x15C^0x11B=0x41)… 七步归约累加 | 0xC1 |
| 2 | Mul(0x02,0x80) | 0x80<<1=0x100，溢出第 9 位，0x100^0x11B | 0x1B |
| 3 | Mul(0x53,0x02) | 0x53<<1=0xA6，最高位为 0 无需归约 | 0xA6 |
| 4 | Mul(0x0E,0x0E) | (x³+x²+x)²=x⁶+x⁴+x²（交叉项特征 2 抵消） | 0x54 |
| 5 | Mul(0x9A,0x9A) | x¹⁴+x⁸+x⁶+x² 归约：x¹⁴≡x⁷+x⁴+x³+x，x⁸≡x⁴+x³+x+1 | 0xC5 |
| 6 | Inv(0x53) | 验证 Mul(0x53,0xCA)=0x01 | 0xCA |
| 7 | Inv(0x02) | xtime(0x8D)=0x11A^0x11B=0x01 | 0x8D |
| 8 | Inv(0x03) | 3·x=1 ⇒ xtime(x)⊕x=1，x=0xF6 时 0xF7⊕0xF6=1 | 0xF6 |

## 二、三问

- (甲) 只左移不归约：0x80<<1=0x100 截回 8 位得 **0x00**（错）；归约后正确值 **0x1B**。
- (乙) 误用 0x11D（归约字节 0x1D）：Inv(0x53) 由 0xCA 错成 **0x8C**；Mul(0x57,0x83) 由 0xC1 错成 **0x31**。
- (丙) log[0]=0 未特判：Mul(0x00,0x53)=exp[log[0]+log[0x53]]=**0x53**（错），正确 **0x00**；Inv(0x00) 必须返回可判定哨兵错误，不得查表得 exp[255-0]=0x01 之类半成品。

## 三、四条不变量 → 代码位置 → 钉住它的测试

1. 与朴素参照一致：field.Mul 经 exp/log 表，与 poly.Reducer.Mul（移位+归约）全 65536 对一致；Inv 满足 Mul(a,Inv(a))==1 → `TestMulMatchesNaive`、`TestInvRoundTrip`（api/api_test.go）。
2. 交换律/分配律：表实现本身满足域公理，采样三元组断言 → `TestFieldAxioms`（api/api_test.go）。
3. 群阶 255：a^255==1，故费马 a^256==a，且 Inv(a)==Pow(a,254)（题面 "Pow(a,255)==a" 与群阶 255 及 Inv==Pow(a,254) 矛盾——a^255=a 仅对 a=1 成立——按一致形式钉住）：field.Pow 平方乘 → `TestFermatLittle`（api/api_test.go）。
4. Inv(0) 报哨兵错误 ErrZeroInverse、不 panic 不返回半成品：field.Inv 开头零值分支 → `TestInvZeroError`（api/api_test.go）。
