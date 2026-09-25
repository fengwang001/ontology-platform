# NOTES - 最长无重复字符子串

## 窗口收缩幅度推导（第三节）
滑动窗口 `[lo, r]` 不变式：窗口内无重复字符。右边界读入 `c`，
若 `c` 上次出现于 `p` 且 `p >= lo`，窗口内出现重复。
- 正确：`lo` 直接跳到 `p+1`，这是恢复不变式的最小收缩。`lo <= p` 时窗口
  必含两个 `c`；`[p+1, r]` 内必无重复（否则上一轮不变式已破，矛盾）。
  跳跃不漏解：任何 `lo' < p+1` 开头的窗口都不可能更优地延伸到 `r`。
- 错误（线上事故）：每次只 `lo++`，一步不足以排除重复，不变式被破坏，
  后续用仍含重复的非法窗口取 max。例 `"abba"`：`r=2` 遇 `'b'`，`lo` 仅
  `0->1`，窗口 `"bb"` 仍重复；`r=3` 时 `last['a']=0 < lo=1` 不再收缩，
  量得 `"bba"` 长 3（应为 2）。左边界滞后，后续窗口全量自错误边界。
- 复杂度：`lo` 单调不减、总移动 <= n；右边界每字符恰访问一次，左边界
  每字符至多一次，合计 <= 2n，单趟 O(n)。

## 代码位置与测试（第二节各条）
- 最长：win/win.go `LongestSubstring`；测试 `TestLongestSubstring`、
  与朴素枚举一致性 `TestAgainstNaive`（对照 check/check.go `Naive`）。
- 区间正确：win/win.go `LongestSubstringRange`；`TestLongestSubstring` 内
  校验区间合法、无重复、长度等于最优。
- 空串返回 0 / 单字符 1 / 全重复 1：`TestLongestSubstring` 表驱动用例
  `""`、`"a"`、`"aaaa"`。
- 确定性：纯函数无随机无全局可变状态，多解时取最先达到的最长窗口。
- 错误实现钉住：`TestBuggyPlusOne`（`buggyPlusOne("abba")==3`，正确值为 2）。
- 复杂度：win/win.go 非导出计数器 `visits`；`TestVisitsLinear`
  断言 n=100000 时访问次数在 [n, 2n]。
- 并发：`TestConcurrent`（8 goroutine 并发查表，`go test -race` 干净）。
- 哨兵错误：str/str.go `ErrEmpty`/`ErrInvalidUTF8`/`ErrTooLong`；
  `TestValidate` 用 `errors.Is` 区分。
