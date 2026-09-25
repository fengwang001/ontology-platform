# NOTES

## "ababab" 推导（6 后缀按字节字典序）
4:"ab" < 2:"abab" < 0:"ababab" < 5:"b" < 3:"bab" < 1:"babab"
SA   = [4 2 0 5 3 1]
LCP  = [2 4 0 1 3]  （ab|abab=2, abab|ababab=4, ababab|b=0, b|bab=1, bab|babab=3）
最长重复 = max LCP = 4；该间隙两侧起点 SA[1]=2、SA[2]=0，取最左 = 0，即 "abab"。
(甲) 改成原始顺序相邻（i 与 i+1）：五对首字节均 a/b 相异，LCP 全 0，长度错成 0；
因为 "abab" 两现于起点 0、2，在原串位置上并不相邻，只查 i/i+1 间隙必然漏掉。
(乙) 直接取 SA[argmax]=SA[1]=2，返回 2；正确最左起点是 0
（须取 min(SA[k],SA[k+1])，并在所有并列最大间隙之间再取 min）。
(丙) 强制两次出现不重叠：长4 "abab"(0,2) 重叠于字节2,3 被排除；长3 "aba"(0,2)、
"bab"(1,3) 也都重叠；最长降为长 2 的 "ab"（起点0与2，区间[0,1]/[2,3]不重叠）。
正确（允许重叠）是长 4 的 "abab"。

## 四条不变量
1 SA 等于 bytes.Compare 全串朴素排序：suffix/suffix.go 的 Build（倍增+基数排序）保证；
  测试钉住：TestSAMatchesNaive（api/api_test.go）。
2 SA 是 0..n-1 排列、LCP[k] 等于逐字符朴素真值：lcp/lcp.go 的 Build（Kasai）保证；
  测试钉住：TestStructuralInvariants（api/api_test.go）。
3 LongestRepeated 等于枚举全部 i<j 后缀对、取最大并列取最左：lcp/lcp.go 的 Longest 保证；
  测试钉住：TestLongestRepeatedNaive（api/api_test.go）。
4 被拒操作不留痕（空串/非法UTF-8/未构建/越界，四个互异哨兵错误）：api/api.go 的 New 只在
  全新对象上构建、查询只读且返回拷贝；测试钉住：TestRejectionLeavesNoTrace（api/api_test.go）。
