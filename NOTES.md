# Manacher 推导与不变量对照

## 第三节推导：s = "abbaxyzabba"（n=11，下标 0..10）

字符： 0:a 1:b 2:b 3:a 4:x 5:y 6:z 7:a 8:b 9:b 10:a

d1（奇回文，中心 i，含半径 0）：

    i    : 0 1 2 3 4 5 6 7 8 9 10
    d1   : 1 1 1 1 1 1 1 1 1 1 1
    最长 : 1 1 1 1 1 1 1 1 1 1 1   起始 = i（长度 1 即单字符自身）

d2（偶回文，中心在 s[i-1]|s[i] 之间，i=0..11）：

    i    : 0 1 2 3 4 5 6 7 8 9 10 11
    d2   : 0 0 2 0 0 0 0 0 0 2  0  0
    i=2  : 长 = 2·2 = 4，起始 = 2-2 = 0 → "abba"
    i=9  : 长 = 2·2 = 4，起始 = 9-2 = 7 → "abba"
全局最长：长度 4，等长取最左 → Start=0, Length=4，子串 "abba"。

- (甲) 只算 d1：最长被错判为 1（任一单字符，如 "a"@0），漏掉 "abba"（正确 4）。
- (乙) 取「最后遇到」会返回起始 7（正确是最左的 0）。
- (丙) 偶长错写 2·d2+1 → 5（正确 4）；奇长错写 2r → 单字符 r=1 错成 2（正确 1）。

## 第二节不变量 → 代码位置 / 钉住它的测试

1. 与朴素一致：`longest.Find` 只按 2r-1 / 2r 公式取最大；`api_test.TestLongestMatchesNaive`
2. 半径自洽：`manacher.Compute` 标准 Manacher 不回退；`manacher_test.TestRadiiSelfConsistent`
3. 最长且最左：`longest.Find` 严格大于才更新、下标升序扫描；`api_test.TestLeftmostOnTie`
4. 失败不留痕：`api.New` 先完整校验再一次性赋值；`api_test.TestRejectedKeepsState`
