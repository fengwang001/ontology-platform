# 安全调查：无引号属性的属性注入

## 现象

在无引号属性（如 `<a class={{x}}>`）中插值时，取值中的某些字节能原样
进入渲染结果，在浏览器解析时终止当前属性值，从而拼出一个新的属性名
（例如 `onclick=...`）。问题只出现在无引号属性；文本、双引号属性、
单引号属性、注释位置均无此问题。

## 根因：两处各自独立的「空白」认定不一致

模块内部对「什么算空白」有两处独立的认定：

1. **扫描器一侧** — `internal/ctxtmpl/chars.go` 中的 `isASCIISpace`，
   被 `internal/ctxtmpl/scanner.go` 用于解析模板、判断无引号属性值在
   哪里结束（`feedUnquoted`、`feedAttrName` 等）。字符集合：

   ```
   { 0x20 ' ', 0x09 '\t', 0x0A '\n', 0x0D '\r', 0x0C '\f', 0x0B '\v' }
   ```

2. **转义器一侧** — `internal/ctxtmpl/escape.go` 中 `escaperFor` 的
   `ctxAttrUnquoted` 转义表里手工维护的空白项。修复前的字符集合：

   ```
   { 0x20 ' ', 0x09 '\t', 0x0A '\n', 0x0D '\r' }
   ```

**差集：`0x0C '\f'`（换页符）和 `0x0B '\v'`（垂直制表符）。**

## 为什么差集能造成属性注入

HTML 规范中，无引号属性值由空白字符终止（空格、Tab、换行、回车、换页
符；部分引擎还认垂直制表符）。扫描器在解析模板时按 `isASCIISpace` 的
完整集合判断属性边界，但渲染插值时，转义表只覆盖其中 4 个字符，于是
`\f` 和 `\v` 被原样写进输出。例如：

```
模板:  <a class={{x}}>
取值:  "a\fonclick=alert(1)"
修复前输出: <a class=a\fonclick=alert(1)>
```

浏览器把 `\f` 当作属性分隔符，于是 `onclick=alert(1)` 成为一个独立的
新属性——属性注入成功。这就是安全团队观察到、而现有测试（只覆盖空格
等常见字符）抓不到的逃逸路径。

## 修复

消除两处认定的不一致，而不是在转义表里手工补漏：`escape.go` 新增
`unquotedEscaper()`，转义表的空白项直接由 `isASCIISpace` 派生
（`&#N;` 形式的十进制实体），扫描器与转义器从此共享同一份空白定义。
非空白项（`& < > " ' \` =`）保持不变，其他上下文的转义行为不受影响。

## 回归防护

- `internal/ctxtmpl/unquoted_space_test.go`：
  - `TestUnquotedAttrEscapesEveryWhitespace` — 对 `isASCIISpace` 认定
    的每一个空白字符各一条用例，断言它在无引号属性里被实体转义、无法
    拼出新属性；
  - `TestWhitespaceDefinitionParity` — 直接断言扫描器的空白集合与
    无引号转义表的空白集合相等，任何一处被改动都会立刻失败；
  - `TestUnquotedAttrKeepsNormalValues` — 正常取值（`abc-123_x`）仍
    原样渲染，防止过度转义。
