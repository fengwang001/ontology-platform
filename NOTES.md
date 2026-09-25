# GF(2^8) / 0x11B 推导与不变量

## 八行表（模 x^8+x^4+x^3+x+1，即归约常数 0x1B）

| 运算 | 结果 | 关键步骤 |
|---|---|---|
| Mul(0x57,0x83) | 0xC1 | 移位异或得 0x7FE1 形态后两次归约（AES 经典值） |
| Mul(0x02,0x80) | 0x1B | 0x80<<1=0x100，溢出第 9 位，^0x11B 归约得 0x1B |
| Mul(0x53,0x02) | 0xA6 | 0x53<<1=0xA6，无溢出，不归约 |
| Mul(0x0E,0x0E) | 0x54 | 特征 2 平方=偶次幂：(x^3+x^2+x)^2=x^6+x^4+x^2 |
| Mul(0x9A,0x9A) | 0xC5 | 平方得 x^14+x^8+x^6+x^2，逐次 ^0x11B 归约 |
| Inv(0x53) | 0xCA | exp[255-log[0x53]]；验证 0x53*0xCA=0x01 |
| Inv(0x02) | 0x8D | x*(x^7+x^3+x^2+1)=x^8+x^4+x^3+x≡1 |
| Inv(0x03) | 0xF6 | 0x03*0xF6=0xF6^(0xF6<<1^0x11B)=0xF6^0xF7=0x01 |

## 三问

- (甲) 只左移不归约：0x100 截回 8 位得 **0x00**（错）；归约后正确值 **0x1B**。
- (乙) 误用 0x11D（低字节 0x1D）：Inv(0x53) 由 0xCA 错成 **0x8C**；Mul(0x57,0x83) 由 0xC1 错成 **0x31**。
- (丙) log[0]=0 未特判：Mul(0x00,0x53)=exp[log[0]+log[0x53]]=**0x53**（错），正确 **0x00**；Inv(0x00) 必须返回可判定错误 ErrZeroInverse，不返回半成品、不 panic。

## 四条不变量 → 代码位置 → 钉住它的测试

1. 与朴素参照一致：field.Mul 走 exp/log 表（field/field.go），朴素移位归约在 poly.Poly.Mul（poly/poly.go）；Inv 满足 a*Inv(a)=1 → TestMulMatchesNaive、TestInvMultiplyIdentity。
2. 交换律/分配律：表实现基于同一循环群，天然满足 → TestFieldAxioms（256×256 交换 + 采样分配）。
3. 群阶 255：a^255=1、a^256=a（费马），且 Inv(a)==Pow(a,254)（题面 "Pow(a,255)==a" 与群阶 255 不自洽，按数值验证后的正确形式钉住）：exp/log 表阶 255（field.buildTables 以 0x03 循环 255 步）→ TestFermatAndInvPow。
4. Inv(0) 报 ErrZeroInverse 且无副作用：field.Inv 入口特判（field/field.go）→ TestInvZeroError。
