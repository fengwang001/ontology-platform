# 安全 findings：无引号属性空白转义不一致

## 现象

在无引号属性（如 `<a c={{x}}>`）中插值时，取值里的个别字符能原样进入渲染结果，
在浏览器里充当属性分隔符，从而拼出一个新的属性名（属性注入）。常见空白
（空格、Tab、换行、回车）都被正确转义，问题只出在一小撮字符上。

## 根因：两处各自独立的「空白」认定

模块内部对「什么算空白」有两处互不一致的定义：

1. **扫描侧**：`internal/ctxtmpl/chars.go` 的 `isASCIISpace`，被
   `internal/ctxtmpl/scanner.go` 用于判断无引号属性值在哪里结束
   （`feedUnquoted`）以及标签内各 token 的分界。其字符集合为：

   ```
   0x20 ' '   0x09 '\t'   0x0A '\n'   0x0D '\r'   0x0C '\f'   0x0B '\v'
   ```

2. **转义侧**：`internal/ctxtmpl/escape.go` 的 `escaperFor(ctxAttrUnquoted)`
   中手工罗列的空白条目，决定无引号属性里哪些字符需要转义。修复前其空白
   子集为：

   ```
   0x20 ' '   0x09 '\t'   0x0A '\n'   0x0D '\r'
   ```

## 差集

```
扫描侧 − 转义侧 = { 0x0C '\f' (form feed), 0x0B '\v' (vertical tab) }
```

## 为什么差集字符能造成属性注入

扫描器把 `\f`、`\v` 当作值结束符来理解模板结构，但转义表把它们原样放行。
于是取值 `1\fonmouseover=evil` 渲染为 `<a c=1\fonmouseover=evil>`：HTML
规范中 form feed 属于 whitespace，浏览器解析到 `\f` 即认为无引号值结束，
随后的 `onmouseover=evil` 被解析成一个全新的属性——属性注入成立。`\v`
虽不在 HTML 规范的 whitespace 之列，但本模块自己的扫描器认定它是空白，
两处定义不一致本身就是漏洞温床（且部分遗留浏览器/解析器也按空白处理），
必须一并消除。

## 修复

不在转义表里手工补字符，而是消除「两处认定」：转义表不再自带空白清单，
`escape.go` 新增 `unquotedEscaper()`，无引号属性的空白条目直接由
`isASCIISpace` 派生（标点部分保留在 `unquotedPunctuationEscaper`）。
从此空白只有 `chars.go` 一处定义，扫描侧与转义侧在构造上恒等。

## 回归保障

- `internal/ctxtmpl/unquoted_ws_test.go`：
  - `TestUnquotedAttrEscapesEveryWhitespace`：对 `isASCIISpace` 认定的每个
    空白字符各一条用例，断言它在无引号属性里被转义、无法拼出新属性；
  - `TestUnquotedWhitespaceParity`：直接断言「扫描侧空白集合 == 转义侧
    空白集合」，任何一处再被改动都会立刻失败；
  - `TestUnquotedAttrKeepsNormalValues`：正常取值 `abc-123_x` 仍原样渲染。
