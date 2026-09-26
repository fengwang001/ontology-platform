# NOTES

## 第三节：八个 token 逐步推导

步骤要点：数字按 `-? int frac? exp?` 分段扫描，`int` 禁前导零、`frac`/`exp` 至少一位数字；字符串先逐字节校验控制字符与转义，再对 `\uXXXX` 做代理对合并。

| token | 类型 | 结果（值或非法原因） |
|---|---|---|
| `0` | 数字 | `0`（int 段取 `0` 分支，合法） |
| `01` | 非法 | 前导零：`0` 后紧跟数字 → `ErrLeadingZero` |
| `-0.5` | 数字 | `-0.5`（负号 + int`0` + frac`.5`，合法） |
| `1e3` | 数字 | `1000`（int`1` + exp`e3`，合法） |
| `1.` | 非法 | 小数部分为空：`.` 后无数字 → `ErrEmptyFrac` |
| `"a\nb"` | 字符串 | `a`+U+000A+`b`（`\n` 转义为换行，长度 3） |
| `"😀"` | 字符串 | U+1F600 `😀`（高代理 D83D + 低代理 DE00 合并） |
| `"\uD800"` | 非法 | 孤立高代理：后无 `\uDC00..\uDFFF` → `ErrLoneSurrogate` |

- (甲) `01`：宽松（允许前导零）解析得 **1**；严格 JSON 文法必须整体拒绝，返回哨兵 **`ErrLeadingZero`**。
- (乙) `"😀"`：正确实现合并为码点 **U+1F600（😀）**；若每个 `\uXXXX` 单独解成 16 位码元不合并，得到两个孤立码元 **U+D83D、U+DE00**（非法代理，无法编码为合法 UTF-8）。
- (丙) `1.`：宽松（小数部分允许为空）解析得 **1**；正确实现返回哨兵 **`ErrEmptyFrac`**。

## 第二节：四条不变量及落点

1. 往返一致：`parse/value.go` 的 `Value.String`（键排序、最短浮点、标准转义）→ 测试 `TestRoundtrip`。
2. 严格拒绝：`lex/lex.go` 的 `ErrLeadingZero/ErrEmptyFrac/ErrEmptyExp/ErrLoneMinus/ErrBadEscape/ErrLoneSurrogate` 与 `parse/parse.go` 的 `ErrDupKey` 互不相同 → 测试 `TestStrictReject`。
3. 与朴素参照一致：`lex.ParseNumber` 逐字符校验文法后取 `strconv.ParseFloat` 正确舍入值（保证不变量 1 对任意文本成立）→ 测试 `TestParseNumberVsNaive`（内置教科书参照，全部向量值一致）。
4. 失败不留痕：`parse.Parse` 出错即返回零 `Value`，解析器状态为每次调用新建、无共享 → 测试 `TestNoPartial`。
