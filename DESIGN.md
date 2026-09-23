# 设计：无限制转置编辑距离与对齐脚本
## 1. 递推式推导
码点序列 a[1..m]、b[1..n]，d[i][j] = 前缀 a[1..i]→b[1..j] 的距离。
常见错误递推（OSA，受限转置）：d[i][j] = min(删/插/替, 若 a[i-1..i] 与
b[j-1..j] 互为逆序则 d[i-2][j-2]+1)。它只允许交换一次、且该子串之后不再被
编辑。CA→ABC：到 (2,3) 时 'A'≠'C'，"CA" 与 "BC" 非逆序，只能删/插/替得 3；
但真实最优是 转置(CA→AC)+插入B = 2，转置产物还要参与后续编辑，故 OSA 错。
正确递推（Lowrance–Wagner）：da[c]=c 在 a[1..i-1] 最后出现的行，db=a[i-1] 在
b[1..j-1] 最后出现的列，i1=da[b[j]]、j1=db：
    d[i][j] = min( d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost,
                   d[i1-1][j1-1] + (i-i1-1) + 1 + (j-j1-1) )
末项 = 删中间 i-i1-1 个 + 转置边界两码点 + 插中间 j-j1-1 个；转置可跨任意距
离、转置后的子串仍可继续编辑，故为无限制语义。CA→ABC 逐格：i=2,j=3 时 i1=1
（a 中 'C' 在行 1）、j1=1（b 中 'A' 在列 1），d[0][0]+0+1+1=2 < 删插替的 3。
## 2. 码点与非法字节
utf8.DecodeRuneInString 解码；返回 (RuneError,1) 的非法字节映射为合成码点
0x110000+字节值：互不相同且超出 U+10FFFF，故 d("\xff","\xfe")==1；Encode 还原。
## 3. 上一次出现位置表
da 是 map[rune]int，按码点值索引，条目数 = 实际出现的不同码点数（≤ m+n），
与 Unicode 码位空间大小无关；除 DP 表外无随输入增长的结构。
## 4. 回溯并列选择规则（确定性）
自 (m,n) 向 (0,0) 回溯，固定优先级：匹配 > 替换 > 删除 > 插入 > 转置；第一
个满足等式的方向被选中，脚本按"从右到左应用"的顺序生成。
## 5. 语义 ↔ 测试对照表
| 语义 | 测试 |
| --- | --- |
| 1 转置样例（CA→ABC=2） | dist.TestDistanceSamples |
| 2 按码点/非法字节互异 | dist.TestDistanceSamples |
| 3 对称/同一/三角不等式 | dist.TestMetricProperties |
| 4 脚本长度=距离且可应用 | align.TestScriptApply、TestTransposeOp |
| 5 确定性 100 次相同 | align.TestScriptDeterministic |
| 6 乘积上限哨兵错误 | dist.TestLimitError、align.TestScriptLimitError |
| 附：计数 ≤ 2(m+1)(n+1) | dist.TestCellCounter |
