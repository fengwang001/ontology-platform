# AUDIT — 语义保证与测试对照

## 第二节八条语义

1. 原子（`?` 单码点、`*` 段内、均不跨 `/`；`\` 转义）：路径按 `/` 切段后段内永无 `/`，见 `engine/engine.go` 的 `Match`/`matchSeg`/`atomMatch` 与 `syntax/syntax.go` 的 `compileSeg`。测试：`engine` `TestMatch` 前三行。
2. `**` 仅整段生效：`syntax.compileSeg` 只在段恰为 `**` 时置 `Double`；`engine.closeOver` 给 `**` 零段 ε 闭包、NFA 自环消费整段。测试：`TestMatch` 中 `a/**/b`、`a**b`、`**.go` 行。
3. 边界与空值：`strings.Split` 不规范化，`x/` 与 `x` 段数不同；空模式编译为单个空段。测试：`TestMatch` 空串行与 `x/**`、`**/x`、`*`、`**` 全表行（同 `DESIGN.md` 表）。
4. 字符类刁钻处：`class/class.go` `Parse`（`]` 首字符字面、`!`/`^` 取反、区间、`\` 转义、取反类不匹配 `/`）。测试：`TestMatch` 类行、`syntax` `TestCompileValid`。
5. 语法错误可判定：`class.Error{Kind, Offset}` 四类 Kind，`syntax.compileSeg` 把段内偏移换算为模式字节偏移；编译失败返回 nil 模式。测试：`syntax` `TestCompileErrors`（含偏移断言）。
6. 复杂度：段级 NFA 状态集合（`engine.Match`）+ 段内只记最后 `*` 回退（`matchSeg`），无指数回溯；`Pattern.steps` 非导出计数器经 `Steps()` 读取。测试：`engine` `TestStepsBound`。
7. 规则集：`set/set.go` `Explain` 遍历全部规则、最后命中者胜；`Undecided`/`Included`/`Excluded` 为三个可区分值。测试：`set` `TestVerdicts`。
8. 热替换与并发：`Set.cur` 为 `atomic.Pointer[version]`，`Replace` 先完整编译再一次性 Store，失败不触碰旧版本；`Explain` 单次 Load，结论/原文/序号/版本同出一版。测试：`TestReplaceKeepsOld`、`TestConcurrentVersions`（`go test -race` 干净）。

## 故障注入与上限

- 截断遍历：`syntax` `TestTruncation` 对 8 个模式的每个字节截断，错误必为四类之一；`[` 后未闭合必为 `KindUnclosed`。
- 非法 UTF-8：`runes.Step` 按单字节 U+FFFD 且保留原字节；测试：`TestMatch` 末两行（`?` 匹配非法字节、`[é-ë]` 拒绝、字面原字节相等）。
- 上限：`syntax.Limits`（字节、`**` 段数）与 `set.Options.MaxRules`；超限报错且旧版本保留。测试：`syntax` `TestLimits`、`set` `TestSetLimits`。

## 第四节实测步数（`TestStepsBound` 日志）

| 模式 | 原子数 | 100 档步数 | 10000 档步数 | 上界 4×原子×码点（小/大） | 倍数 |
|---|---|---|---|---|---|
| `a*a*a*a*a*a*a*a*b` | 18 | 100 | 10000 | 7200 / 720000 | 100 |
| `**/**/**/**/**/x` | 6 | 100 | 10000 | 4776 / 479976 | 100 |

两档均远低于上界，且大档步数为小档的 100 倍（≤ 200），呈线性而非指数。
