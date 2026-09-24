# 严格模式流式 Base64 设计

## 规范形式的推导（语义第 2 条）

Base64 把 3 字节（24 bit）编码为 4 个 6 bit 字符。输入长度非 3 的倍数时：

- 余 1 字节（8 bit）：高 6 bit 为第 1 字符，剩 2 bit 左移补 4 个 0 凑成第 2 字符，尾部补 `==`。
  第 2 字符只有高 2 bit 有效，低 4 bit 不承载任何输入数据。
- 余 2 字节（16 bit）：6+6 之后剩 4 bit，第 3 字符高 4 bit 有效、低 2 bit 是填充 0，尾部补 `=`。

宽松解码器忽略这些「未使用比特」，于是字节串 `A` 同时对应 `QQ==`、`QR==`、`QS==`、`QT==`
四个编码——编码不唯一，Decode 的像集中出现一对多。严格模式要求在接受集上
`Encode(Decode(y)) == y`（Decode 是 Encode 的左逆、Encode 是 Decode 的右逆），
这等价于「任意字节串的编码唯一」。因此必须拒绝未使用比特非零的尾组：

- 尾组形如 `xx==`：第 2 个字符的低 4 bit 必须为 0。`QR==` 中 `R`=17=0b010001，低 4 bit=0001，拒绝。
- 尾组形如 `xxx=`：第 3 个字符的低 2 bit 必须为 0。`QUJ=` 中 `J`=9=0b001001，低 2 bit=01，拒绝。

结构性规则：`=` 只允许出现在最后一个组，且形态只能是 `xx==` 或 `xxx=`；
流长度（去除合法换行后）必须是 4 的倍数，故 `QQ`、`QQ=`、`QQ===` 均拒绝；
`=` 之后出现任何字符（含 `=` 自身）即填充位置错误，故 `QQ==QQ==` 拒绝。
空输入合法：0 是 4 的倍数，无尾组可检查。

**判定所用比特**：仅尾组第 2 字符的低 4 bit（`xx==` 情形）与第 3 字符的低 2 bit
（`xxx=` 情形），共两处检查，其余组全部 6 bit 均有效、无需检查。

## 语义与测试对照表

| 第二节语义 | 钉住它的测试函数 |
|---|---|
| 1 标准字母表+`=`填充、MIME 76 换行 | `TestEncodeGroupStdlib`、`TestRoundTrip`、`TestMIMELayout` |
| 2 规范形式（非规范尾部拒绝） | `TestStrictSamples`、`TestDecodeGroup` |
| 3 换行位置规则 | `TestNewlineRules` |
| 4 五类错误可区分+字节偏移 | `TestErrorKinds` |
| 5 跨切分点一致 | `TestSplitInvariance`、`TestCheckedCounter`（1 字节一段） |
| 6 往返（两种 MIME 设置） | `TestRoundTrip` |
| 7 输出上限（组边界、终态） | `TestOutputLimit` |
| 复杂度：计数器==输入字节数 | `TestCheckedCounter` |
