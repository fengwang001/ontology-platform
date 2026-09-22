# 无引号属性属性注入：两处「空白」认定不一致

## 现象

在无引号属性（`<a c={{x}}>`）中插值时，取值可以携带换页符 `\f`（0x0C）
或垂直制表符 `\v`（0x0B）从属性值中"逃出来"，在渲染结果里拼出新的
属性名，例如值 `abc\fonload=x` 渲染为 `<a c=abc\fonload=x>`，其中
`\f` 未被编码，等价于空格分隔，`onload` 成为一个新属性。空格、Tab、
LF、CR 都被正确转义，因此常规探测发现不了。

## 根因：两处各自独立、互不相同的空白认定

1. 扫描模板、判断属性在哪里结束的一侧（修复前）
   - 位置：`internal/ctxtmpl/chars.go`，函数 `isASCIISpace`
   - 被 `internal/ctxtmpl/scanner.go` 的 `feedUnquoted`、`feedAttrName`、
     `feedAttrEq`、`feedTagName`、`feedTagGap` 等状态使用
   - 字符集合（6 个）：空格 0x20、Tab 0x09、LF 0x0A、CR 0x0D、
     FF（换页 `\f`）0x0C、VT（垂直制表符 `\v`）0x0B

2. 决定无引号属性里哪些字符需要转义的一侧（修复前）
   - 位置：`internal/ctxtmpl/escape.go`，函数 `escaperFor` 的
     `ctxAttrUnquoted` 转义表
   - 被编码的空白字符（4 个）：空格 0x20、Tab 0x09、LF 0x0A、CR 0x0D

## 差集

- 扫描集合 − 转义集合 = { 0x0C（`\f` FF）, 0x0B（`\v` VT）}
- 反向差集为空。

## 为什么差集字符能造成属性注入

扫描器把 `\f`/`\v` 当作空白：遇到它们就认为无引号属性值结束、回到
标签间隙状态，因此后续字面量（如新属性名、`=`）会被当成标签结构继续
解析；而转义表不认识这两个字节，插值值中的它们原样写入输出。于是
"扫描器认为值已经结束"与"输出中该字节仍是裸的分隔符"同时成立——
浏览器按空白切分无引号属性值时，该字节后面的内容就成为一个新属性。
这不是转义表漏列两笔的问题，而是同一份语义（"什么算空白"）在模块里
被手写了两次且没有约束它们相等。

## 修复

- `internal/ctxtmpl/chars.go` 新增唯一事实来源 `asciiSpaceBytes`
  （空白集合）与 `asciiSpaceEntities`（同一集合对应的实体），
  `isASCIISpace` 改为遍历该列表。
- `internal/ctxtmpl/escape.go` 的 `ctxAttrUnquoted` 表不再手写空白行，
  而是从 `asciiSpaceEntities` 复制，保证转义的字节集合与扫描器终止
  无引号值的字节集合必然相同。未新增对其他字符的转义，
  `abc-123_x` 等正常取值仍原样渲染。
- 回归：`internal/ctxtmpl/unquoted_space_test.go`
  - `TestUnquotedAttrEscapesEveryScannerWhitespace`：对扫描器认定的
    每一个空白字符各一条用例；修复前 0x0C/0x0B 两条失败。
  - `TestWhitespaceSetsAgree`：直接断言两侧空白集合相等，并对 0..255
    逐字节核对转义行为与集合归属一致。
  - `TestUnquotedAttrNormalValueUnchanged`：正常取值不被过度转义。
