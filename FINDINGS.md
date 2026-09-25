# FINDINGS: argv 解析器边界行为与文档承诺的不一致

以下每条均由 `argv/characterization_test.go` 中的表驱动测试钉住
（断言的是**当前真实行为**，全部通过）。行号对应 `argv/parse.go`。

## 1. 布尔长标志带 `=value` 被静默吞掉

- 复现输入：`--verbose=false`（`verbose` 为 Bool 标志）
- 实际输出：无错误；`Bool("verbose")==true`，`=false` 被完全忽略
- 应当输出：报错（Bool 不接受值），或至少解析 `=false` 为 false
- 根因：`longFlag` 先按 `=` 拆出 `name`/`value`/`hasValue`，但 Bool
  分支直接 `p.res.bools[spec.Long] = true; return nil`，从不检查
  `hasValue`。任何 `=任意值`（含空串）都被丢弃且不报错。
- 测试：`TestBoolLongFlagWithEqualsValue`（遍历
  `false/true/0/no/"" /anything` 六种取值）

## 2. 布尔标志不消费空格分隔的下一个 token

- 复现输入：`--verbose true`（或 `-a true`、`-ab true`）
- 实际输出：`verbose==true`，且 `"true"` 成为位置参数
- 应当输出：与 String 标志「缺值吃下一个参数」的语义保持一致，
  或文档明确说明 Bool 后 token 一律是操作数
- 根因：`longFlag`/`shortFlags` 的 Bool 分支取值前直接返回，只有
  String 分支有 `*i++; value = args[*i]` 的消耗逻辑。Parse 文档注释
  只写 "`--name=value` 和 `--name value` 等价"，未限定该等价仅对
  String 成立，用户易误以为 `--verbose false` 能赋值。
- 测试：`TestBoolFlagDoesNotConsumeNextToken`

## 3. String 标志把 `--` 终止符吞成自己的值

- 复现输入：`--output -- --verbose`（短形式 `-o -- --verbose` 同理）
- 实际输出：`output=="--"`；`--` 不再终止标志解析，后续
  `--verbose` 仍被解析为标志（`verbose==true`）；无位置参数
- 应当输出：`--` 应优先被识别为终止符：`--output --` 应报
  `ErrMissingValue`（或 output 缺省），`--verbose` 应成为位置参数
- 根因：`longFlag`/`shortFlags` 缺值时无条件 `value = args[*i+1]`，
  不检查下一个 token 是否为 `--`。终止符判断只发生在 Parse 主循环
  的 token 分发层，被吞掉的 `--` 永远到不了那一层。文档承诺
  "a lone `--` terminates flag parsing"，此处失效。
- 测试：`TestStringFlagSwallowsDoubleDashTerminator`

## 4. 重复布尔标志一律 ErrDuplicate，无幂等语义

- 复现输入：`--verbose --verbose`、`-a -a`、合并序列 `-aa`
- 实际输出：`ErrDuplicate`，Result 为 nil
- 应当输出：布尔标志幂等（重复出现与出现一次等价）是命令行惯例
  （如 `grep -vv`、标准库 flag 的重复布尔），至少文档应明确
  「同一 Bool 标志出现两次即错误」
- 根因：`checkDuplicate` 对任何 Kind 的第二次出现一律报错，
  不区分 Bool（幂等安全）与 String（重复取值有歧义）。包注释与
  Parse 注释均未说明重复标志的处理策略。
- 测试：`TestDuplicateBoolFlagRejected`

## 5. 多个 Required 同时缺失时错误非确定

- 复现输入：specs 含 Required 的 `alpha`/`beta`/`gamma`，args 为空
- 实际输出：返回的 `ErrRequired` 指向哪个标志随 map 迭代序变化。
  实测 200 次分布：`map[alpha:147 beta:30 gamma:23]`
- 应当输出：确定性行为——按 specs 声明顺序报告第一个缺失的
  Required 标志
- 根因：Parse 末尾 `for _, s := range p.byLong` 遍历 map 检查
  Required，Go map 迭代序随机，命中哪个缺失标志就报哪个。
  调用方无法依赖错误消息定位具体缺失项，测试也无法稳定断言。
- 测试：`TestMultipleMissingRequiredIsNondeterministic`（循环 200
  次，断言错误分类稳定为 ErrRequired 且指名在缺失集合内，分布
  仅作日志记录）

## 备注

- 本次只新增 `argv/characterization_test.go` 与本文件，未改动
  `parse.go`/`spec.go`/`result.go` 的任何行为。
- 上述「应当输出」为与注释/惯例对齐的建议方向，具体修复策略
  （报错 vs 兼容）需另行决策。
