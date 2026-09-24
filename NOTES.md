# 失配表推导与不变量对照

## 推导：ababaca 的失配表
f[k] = 模式前 k+1 个字符的「最长真前缀且同时是后缀」的长度。
起点：第 0 项 f[0]=0（单字符无真前缀）；第 1 项 f[1]=0（"ab" 无公共前后缀）。
回退方向：失配时已匹配长度 j 回退到表值 f[j-1] 本身，**不是**减一。

```
k:     0   1   2    3     4      5       6
前缀:   a   ab  aba  abab  ababa  ababac  ababaca
f[k]:   0   0   1    2     3      0       1
```

## 走查：t=abababaca，p=ababaca
i=0..4 相等，j 到 5；i=5 时 'b'≠p[5]='c' 失配，j 从 5 回退到 f[4]=3，文本指针停在 5；
重比 t[5]='b'=p[3]='b'，j=4；i=6,7,8 相等，j 到 7，在 i=8 报出起点 2；j 回退到 f[6]=1，结束。结果 [2]。

## 两种错误写法
- 减一（j=f[j-1]-1）：第 6 步（i=5 首次失配）j 落到 2 而非 3，继续错位，漏掉位置 2 的匹配（得 []）。
- 第 1 项设成 1：1 不小于前缀长度 2 且 "a" 非 "ab" 后缀，违反不变量 3，SelfCheck 直接拒绝；
  且 j=1 失配时回退不再严格下降，文本指针停滞、比较次数失控（不再 ≤2n）。

## 不变量对照
1. 找全且不多找：scan/scan.go KMP 主循环（报匹配后 j=f[m-1] 继续，含重叠）；find.TestFindAllMatchesNaive。
2. 文本指针不回退：scan/scan.go 中 i 只自增；scan.TestScanLinear（advances==n 且 compares<=2n）。
3. 失配表自洽：find/find.go SelfCheck 逐位核验 f[k]<k+1 且该段前缀=后缀；find.TestSelfCheck。
4. 失败不留痕：find/find.go Compile 先校验再构造、拒绝时返回 nil；find.TestRejectedCompileKeepsState。
并发：Compile 后 Matcher 只读；find.TestConcurrentFindAll（go test -race 干净）。
