# 带转置的编辑距离：设计推导

## 1. 转置语义推导（CA→ABC 为什么是 2）

常见错误递推（OSA，受限转置，只转置紧邻两字符且之后不得再编辑）：
    if a[i]==b[j-1] && a[i-1]==b[j]:
        d[i][j] = min(d[i][j], d[i-2][j-2]+1)
按它算 d("CA","ABC")：d[1][1]=1，d[1][2]=2，d[2][1]=2，d[2][2]=2；
d[2][3] 处转置候选 d[0][1]+1=2，但普通三项最小为
min(d[1][2],d[1][3],d[2][2])+1=3，故 OSA 得 3。错因：它要求转置块
整体不再被编辑；无限制语义下最优脚本是 删B(1)+转置CA(1)=2，即
转置两锚点之间的字符允许继续被删/插。
本题递推（真·Damerau-Levenshtein，Lowrance–Wagner）：记 i1=字符
b[j] 在源串第 i 行前最后一次出现的行，j1=字符 a[i] 在目标串第 j
列前最后一次出现的列，则
    d[i][j] = min( d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost,
                   d[i1-1][j1-1] + (i-i1-1) + (j-j1-1) + 1 )
转置项=删两锚点间 i-i1-1 个源字符 + 插 j-j1-1 个目标字符 + 交换
两锚点 1 次。重算 d[2][3]：i1=1（a[1]=C==b[3]），j1=2（b[2]=A
==a[2]），候选=d[0][1]+0+0+1=2，故 d("CA","ABC")=2。该递推把每
次编辑视为独立单字符操作，故天然满足三角不等式。

## 2. 码点、非法字节与并列规则

按码点比较；每个非法 UTF-8 字节 b 映射为互不相同的合成码点
-1-b（负值 rune），故 d("\xff","\xfe")==1，Encode 可还原原字节。
等长最优脚本的并列选择固定为：匹配 > 替换 > 删除 > 插入 > 转置，
同分先命中者优先，故同一输入的脚本唯一确定。

## 3. 语义 ↔ 测试对照表

| 语义 | 钉住它的测试 |
| --- | --- |
| 1 转置样例（含 CA→ABC=2） | TestDistanceTable / TestScriptTable |
| 2 码点与非法字节 | TestDistanceTable、TestInvalidByteRoundTrip |
| 3 对称/同一/三角不等式 | TestSymmetryIdentity、TestTriangleInequality |
| 4 脚本长度=距离、可应用、转置相邻 | TestScriptTable、TestScriptExhaustive |
| 5 确定性（100 次逐项相同） | TestDeterministic |
| 6 上限可判定错误 | TestLimit、TestScriptLimit |
