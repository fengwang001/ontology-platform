# NOTES

## 八个 token 推导（规则叠加结果）
| token | 类型 | 结果 |
|---|---|---|
| `0` | 数字 | 0 |
| `01` | 数字 | 非法：0 后紧跟数字 1，前导零 → ErrLeadingZero |
| `-0.5` | 数字 | -0.5 |
| `1e3` | 数字 | 1000（1×10^3） |
| `1.` | 数字 | 非法：小数点后无数字，空小数 → ErrEmptyFrac |
| `"a\nb"` | 字符串 | 0x61 0x0A 0x62（转义 `\n` 合并为换行符） |
| `"😀"` | 字符串 | U+1F600 😀（等价 `😀` 经代理对合并） |
| `"\uD800"` | 字符串 | 非法：高代理后无低代理跟随 → ErrLoneSurrogate |

(甲) 宽松前导零解析会把 `01` 读成 **1（错值）**；严格文法整体拒绝，返回哨兵 **ErrLeadingZero**。
(乙) 正确实现把 `😀` 合并为 **U+1F600（😀）**；若每个 `\uXXXX` 单独当 16 位码元、不合并，得到两个孤立代理 **U+D83D、U+DE00**（非法 Unicode 串）。
(丙) 宽松解析（可选小数部分允许为空）把 `1.` 读成 **1（错值，空小数被静默吞掉）**；严格实现返回 **ErrEmptyFrac**，整体拒绝。

## 第二节四条不变量：保证位置 + 钉住测试（均在 parse/parse_test.go）
1. 往返一致：`parse/value.go` 的 `Value.String()`（键排序、控制字符转 `\u00xx`）+ `api/api.go` 的 `Stringify`（重解析校验）；由 **TestRoundTrip** 钉住。
2. 严格拒绝：`lex/lex.go` 的 ErrLeadingZero/ErrEmptyFrac/ErrEmptyExp/ErrLoneMinus/ErrBadEscape/ErrLoneUnicode/ErrLoneSurrogate + `parse/parse.go` 的 ErrDuplicateKey，八个哨兵互不相同；由 **TestRejectSentinels**、**TestNumberVectors**、**TestStringVectors** 钉住。
3. 朴素参照一致：`lex/lex.go` 的 ParseNumber 逐字符扫描 int/frac/exp 手动累加；由 **TestParseNumberNaiveConsistency**（表内向量 + 300 个生成向量对照 naiveNum）钉住。
4. 失败不留痕：`parse/parse.go` 的 Parse 全程局部构建、任何错误返回 `Value{}`；由 **TestFailureLeavesNoTrace** 钉住。
复杂度（第四节）：非导出字段 `parser.cmp`，map 判重每键恰好 1 次探测；**TestDupCheckLinear** 在包内测试直接读字段（不经导出接口），断言 m=100/1000/10000 档 cmp==m（线性，非 m²）。
并发（第六节）：**TestConcurrent** 起 32 个 goroutine 并发解析同一只读文本并各自序列化，与串行结果一致；`go test -race` 干净。
