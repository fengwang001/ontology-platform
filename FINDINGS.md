# 安全调查：无引号属性空白转义不一致

## 结论

模块内部对「什么算空白」存在两处各自独立的认定，二者集合不同。差集
中的字符 `\v`（0x0B）和 `\f`（0x0C）被扫描器视为无引号属性值的结束
符，却被转义表原样输出，从而能在渲染结果里拼出新的属性名。

## 两处「空白」认定的位置

- **扫描侧**：`internal/ctxtmpl/chars.go` 中的 `isASCIISpace`。
  扫描器（`internal/ctxtmpl/scanner.go` 的 `feedUnquoted`、
  `feedAttrName`、`feedAttrEq` 等）用它判断无引号属性值在哪里结束、
  新属性从哪里开始，也用于 URL 属性前缀的空白剥离。
  认定的集合（6 个字符）：
  `0x20` 空格、`0x09` Tab、`0x0A` 换行、`0x0D` 回车、
  **`0x0B` 垂直制表 `\v`**、**`0x0C` 换页 `\f`**。

- **转义侧**：`internal/ctxtmpl/escape.go` 中 `escaperFor` 的
  `ctxAttrUnquoted` 表（修复前为手写字面量）。
  认定的集合（4 个字符）：
  `0x20`、`0x09`、`0x0A`、`0x0D`。

## 差集

扫描侧有而转义侧没有：**`\v`（0x0B）** 和 **`\f`（0x0C）**。

## 为什么差集字符能造成属性注入

扫描器的状态机认定：无引号属性值遇到这 6 个空白字符中的任何一个就
结束，其后的字节按「新属性名」解析。也就是说，渲染器的整个安全模型
假设这些字符绝不会以字面形式出现在无引号属性值的输出里——这个假设
要靠转义表来保证。但转义表漏掉了 `\v` 和 `\f`，于是：

- 取值 `v\fonclick=pwn` 渲染为 `<a c=v` + 原始 `\f` + `onclick=pwn>`。
  `\f` 是 HTML5 标准定义的空白字符，浏览器在解析时会在它处断开属性
  值，把 `onclick=pwn` 当作一个独立的新属性——注入成功。
- `\v` 虽不是 HTML5 空白，但扫描器自己把它当作属性分隔符：它会认为
  插值已经结束、后续内容处于「属性间隙」状态，从而使其安全决策
  （如 URL 属性的 scheme 前缀跟踪）建立在错误的上下文上。两处认定
  必须一致，否则任何依赖扫描器状态机的防护都可能被绕过。

双引号、单引号、文本、注释位置不受影响：带引号的属性值只由对应引号
结束，文本/注释的转义目标也不是空白字符。

## 修复方式

不在大转义表里手工补字符，而是把无引号属性的空白转义抽成唯一的命名
集合 `unquotedWhitespaceEscapes`（`internal/ctxtmpl/escape.go`），
`escaperFor` 的 `ctxAttrUnquoted` 分支由它构建；同时新增
`TestWhitespaceDefinitionsAgree`
（`internal/ctxtmpl/whitespace_test.go`），直接断言
`isASCIISpace` 与实际转义表的空白集合相等。此后任何一侧被改动，
该测试立即失败。

## 回归测试

- `TestRenderUnquotedAttrEscapesEveryWhitespace`：对模块认定为空白
  的每一个字符各一条用例，断言它在无引号属性里被转义、无法拼出
  新属性。修复前 `\v`、`\f` 两条失败，修复后全部通过。
- `TestWhitespaceDefinitionsAgree`：两处空白认定的集合一致性断言。
