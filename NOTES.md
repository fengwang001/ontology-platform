# NOTES

演示参数 B=3、单模 M=7，s="abab"（'a'=97≡6, 'b'=98≡0 mod 7）。
P[0]=0，P[i]=(P[i-1]*3+c[i-1]) mod 7；pow[i]=3^i mod 7。五行表：

| i | P[i] | 3^i mod 7 |
|---|------|-----------|
| 0 | 0    | 1         |
| 1 | 6    | 3         |
| 2 | 4    | 2         |
| 3 | 4    | 6         |
| 4 | 5    | 4         |

- (甲) s[2..4)="ab"：5 − 4·3^2 = 5−8 = −3 ≡ **4**。若漏掉位移项写成 P[4]−P[2]，得 5−4 = **1**（错值）。
- (乙) 原始差即 **−3**；Go 的 (-3)%7 = **−3**（负余数，错值），必须加 M 调整，正确为 **4**。
- (丙) "aba"=P[3]−P[0]·3^3 = 4；"bab"=P[4]−P[1]·3^3 = 5−36 = −31 ≡ 4，两者都是 **4**，单哈希碰撞。真实实现用双哈希（须两个模同时碰撞），否则 Equal(0,3,1,4) 会把不等子串**误判为 true**。

## 不变量落点

1. 与朴素参照一致：`query/query.go` Equal 先比长度再比双哈希；测试 `TestEqualNaive`（query/query_test.go）。
2. 子串哈希正确：`rhash/rhash.go` Hash 严格按 (P[r]−P[l]·B^len) 取模并回正；测试 `TestSubstringHash`（rhash/rhash_test.go）。
3. LCP 正确：`query/query.go` LCP 上中位数二分 + Equal；测试 `TestLCPNaive`（query/query_test.go）。
4. 失败不留痕：`api/api.go` New 先拒空串、成功末尾才整体原子替换；Equal/LCP 先校验再查询；哨兵与零值拒绝在 `query/query.go`；测试 `TestErrorsNoTrace`（query/query_test.go，白盒）与 `TestAPIErrors`（rhash/rhash_test.go，外部包测 api 全局态）。
