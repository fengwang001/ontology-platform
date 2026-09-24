# 失配表推导与不变量

## 推导：模式 ababaca 的失配表
定义：fail[i] = 模式前 i 个字符的「最长真前缀且同时是后缀」的长度；起点 fail[0]=fail[1]=0。
逐位（i=1..7）："a"→0，"ab"→0，"aba"→1，"abab"→2，"ababa"→3，"ababac"→0，"ababaca"→1。
整张表一行：**0 0 1 2 3 0 1**。失配时把已匹配长度 k 直接回退到 fail[k]（不是 fail[k]-1）。

## 走查：文本 abababaca（文本指针 j 只增不减）
- j=0..4 逐字符匹配，k 升到 5；j=5 处 'b'≠'c' 失配：k 由 5 回退到 fail[5]=3，j 停在 5 不动。
- j=5..8 依次匹配 b、a、c、a，k 升到 7（=模式长）：命中起始位置 j-7=2；随后 k 回退到 fail[7]=1，j=9 扫描结束。
- 结果 [2]，与朴素逐位比对一致。

## 两种错误写法的后果
- 回退写成「减一」（k=fail[k]-1）：j=5 失配时 k 由 5→2，下一步再失配得 k=-1（非法下标）；
  即使把 -1 钳到 0 继续，也会漏掉位置 2 的匹配，结果变成 [] 而非 [2]。
- 第 1 项设成 1（fail[1]=1）：构造被污染，整表变成 1,2,3,4,5,6,7（不再严格小于 i，违反真前缀）；
  j=5 失配时 k 由 5 回退到 fail[5]=5 不变，同一文本位置无限循环，文本指针永远停在 5。

## 四条不变量及其保证位置与测试
1. 找全且不多找：`scan/scan.go` 的 `Matcher.Scan` 单遍产出全部命中（含重叠），`find/find.go` 的
   `FindAll` 原样返回；测试 `TestFindAllMatchesNaive` 与测试文件内的朴素 `naive` 逐元素对照。
2. 文本指针不回退：`scan/scan.go` 的 `Scan` 中 j 只有 `j++`；`Matcher` 内非导出计数器记录推进与
   比较次数；测试 `TestScanIsLinear` 断言推进次数恰为 n 且比较次数 ≤2n。
3. 失配表自洽：`find/find.go` 的 `SelfCheck` 逐位核验 fail[i]<i 且所述前缀确为该前缀的后缀；
   测试 `TestSelfCheck` 与 `TestTableAbabaca` 钉住。
4. 失败不留痕：`find/find.go` 的 `Compile` 校验失败直接返回 nil+哨兵错误，`FindAll` 在扫描前
   拒绝过长文本，均不触碰已编译状态；测试 `TestRejectedOpsLeaveNoTrace` 验证被拒后结果不变。
