# AUDIT

## 第二节语义逐条核对（位置 / 钉住它的测试）

1. 原子语义：`?` 单码点、`*` 段内、取反类不匹配 `/`、`\` 转义 —— `engine/engine.go` 的
   `segMatch`/`atomAt`、`syntax/syntax.go` 的 `parseAtoms` —— `TestSemantics`、`TestClasses`。
2. `**` 仅整段生效，相邻 `*` 坍缩 —— `syntax.Compile`（`s == "**"` 才置 `Glob`）与
   `engine.nfa`/`closure` —— `TestSemantics` 中 `a/**/b`、`x/**`、`a**b`、`**.go` 行。
3. 边界与空值：不规范化、空串=单空段、空模式只配空串 —— `strings.Split` 直切、
   `syntax.Compile` 空段即空原子列 —— `TestSemantics` 中 `""`、`x/`、`a//b` 行。
4. 字符类刁钻处 —— `class/class.go` 的 `Parse`（`]` 作首字符、`-` 收尾、码点区间、转义）
   —— `TestClasses`、`TestSyntaxErrors`。
5. 可判定错误带字节偏移 —— `class.Error{Kind, Offset}`，`syntax` 透传并加段基址
   —— `TestSyntaxErrors`（四类 + 偏移断言）、`TestTruncation`（逐字节截断循环）。
6. 复杂度：段内末星回退 + 跨段 NFA 子集，计数器 `engine.steps`（`LastSteps` 读取）
   —— `TestSteps`。
7. 规则集：顺序包含/排除、最后命中者胜、未决可区分 —— `set/set.go` 的 `Explain`
  （顺序遍历、后者覆盖、`Undecided` 独立取值）—— `TestRules`。
8. 热替换与并发：`set` 用 `atomic.Pointer` 指向不可变 version，`Replace` 全部编译成功才
   `Store`，失败不动旧指针 —— `TestReplaceKeepsOld`、`TestConcurrentVersions`（`go test -race` 干净）。

## 第四节实测步数（`TestSteps` 断言钉住）

| 模式（原子数） | 路径档 | 实测步数 | 上界 4×原子×码点 |
|---|---|---|---|
| `a*a*a*a*a*a*a*a*b`（17） | 100 码点 | 109 | 6 800 |
| 同上 | 10 000 码点 | 10 009 | 680 000 |
| `**/**/**/**/**/x`（7） | 100 段（199 码点） | 700 | 5 572 |
| 同上 | 10 000 段（19 999 码点） | 70 000 | 559 972 |

放大比：91.8 与 100.0，均 ≤ 200，且 ≤ 4×原子×码点，无线性以上增长。
