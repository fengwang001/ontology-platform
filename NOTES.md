# NOTES

## 窗口收缩幅度推导（第三节）

滑动窗口 [lo, hi) 内维护「无重复字符」不变量。右边界 hi 读入字符 c 时，
若 c 上一次出现位置 p 满足 p >= lo，则窗口内已有重复，必须收缩左边界。

- 错误做法：lo 每次只 +1。反例 "abba"：hi 推进到第二个 'b'（位置 2）时
  只把 lo 从 0 挪到 1，窗口 "bb" 仍含重复，不变量被破坏；随后第二个 'a'
  （位置 3）与位置 0 的 'a' 比较时 p < lo 被忽略，窗口被误当作合法，
  最终在 "abba" 上返回 3（正确答案是 2），结果不合法；对「重复字符在
  窗口内偏后」的输入（如线上事故）则会因收缩滞后而漏掉更长的合法窗口，
  返回偏短结果。
- 正确做法：lo 直接跳到 p+1（重复字符上一次出现位置的下一个位置）。
  跳跃后窗口 (p, hi] 内不含 c 的旧出现，且 (p, hi) 原本就无重复，故不变量
  恢复；同时 p+1 是允许的最小 lo——任何 <= p 的 lo 都仍包含旧 c，不可能
  合法，所以跳跃不会漏掉任何以 hi 结尾的合法窗口，最长解必然被枚举到。
- 复杂度：lo 单调不减、每字符最多跳过一次，hi 每字符访问一次，总访问量
  <= 2n，单趟 O(n)。测试用 win 包非导出计数器在 n=100000 下断言该上界。

## 语义落点（第二节）

1. 最长：win/win.go LongestSubstring；check.TestTableAndRange、TestExhaustive 对照 check.Naive。
2. 区间：win/win.go LongestSubstringRange；TestTableAndRange 验 check.Distinct 且长度一致。
3. 空串 0 与 5. 单字符/全重复 1：check/check_test.go cases 表，TestTableAndRange。
4. 确定性：TestTableAndRange 二次调用区间相同，长度与 LongestSubstring 自洽。
另：第四节复杂度上界见 TestVisitBoundAndConcurrent（同函数验第五节并发 -race）。
