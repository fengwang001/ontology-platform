# FINDINGS: argv 解析器边界行为与文档承诺的不一致

测试文件：`argv/characterization_test.go`（表驱动，断言当前真实行为，全部通过）。
实现位置：`argv/parse.go`。以下每条给出复现输入、实际输出、应当输出、根因。

## 1. 布尔长标志带 `=value` 被静默吞掉

- 复现：`Parse(specs, []string{"--verbose=false"})`（`verbose` 为 Bool）
- 实际：`err == nil`，`Bool("verbose") == true`，`--verbose=`、`--verbose=banana` 同理；
  值被完全忽略，既不报错也不生效。
- 应当：要么解析 `=false` 为 false，要么对 Bool 带值返回错误。静默吞值让
  `--verbose=false` 与 `--verbose` 同义，用户拼写错误无法被发现。
- 根因：`longFlag`（parse.go）先按 `=` 拆出 `name`/`value`/`hasValue`，但
  `spec.Kind == Bool` 分支直接 `bools[name] = true; return nil`，从不检查
  `hasValue`。
- 文档冲突：注释只承诺「`--name=value` 与 `--name value` 等价」，未说明 Bool
  带值的行为。

## 2. 布尔标志不消费空格分隔的下一个 token

- 复现：`Parse(specs, []string{"--verbose", "true"})`
- 实际：`verbose == true`，且 `"true"` 变成位置操作数（`Operands() == ["true"]`）。
- 应当：行为本身符合「Bool flags take no value」的常规约定，但与 String 标志
  「缺值吃下一个参数」形成不对称；用户从 String 标志迁到 Bool 标志时，残留的
  值 token 会悄悄变成操作数。文档应明确说明 Bool 永不消费下一个 token。
- 根因：`longFlag`/`shortFlags` 的 Bool 分支直接返回，不看下一个参数（这是
  设计使然，但无文档说明）。

## 3. String 标志把 `--` 终止符吞成自己的值

- 复现：`Parse(specs, []string{"--output", "--", "--verbose"})`；短形式
  `[]string{"-o", "--", "--verbose"}` 同理。
- 实际：`String("output") == "--"`；`--` 的终止语义丢失，后面的 `--verbose`
  仍被当标志解析（`verbose == true`），操作数为空。
- 应当：`--` 应优先被识别为终止符（对 `--output --` 报 `ErrMissingValue`，
  或让 `--` 保持终止语义），其后内容一律为操作数。
- 根因：`longFlag`/`shortFlags` 在 String 缺值时无条件 `*i++; value = args[*i]`，
  不检查下一个 token 是否为 `--`；主循环里 `arg == "--"` 的终止判断因此永远
  看不到被吞掉的那个 `--`。
- 文档冲突：注释承诺「A lone `--` terminates flag parsing」，但被吞时该承诺
  失效。

## 4. 重复布尔标志一律 `ErrDuplicate`，无幂等重复语义

- 复现：`Parse(specs, []string{"--verbose", "--verbose"})`、`{"-vv"}`、
  `{"-v", "-v"}`、`{"--verbose", "-v"}`。
- 实际：全部返回 `ErrDuplicate` + nil Result。
- 应当：Bool 标志重复是幂等的（置 true 两次无副作用），按惯例（含标准库
  `flag`）应允许；至少文档应说明「任何标志重复都是错误」。
- 根因：`checkDuplicate` 对 `set[name]` 已为 true 的任何标志一律报错，不区分
  Kind；合并序列 `-vv` 中第二次出现的同一 Bool 短标志也走同一路径。
- 文档冲突：`ErrDuplicate` 注释为「flag provided more than once」，但未说明
  Bool 幂等重复也算错误。

## 5. 多个 Required 同时缺失时，报错哪个标志不确定

- 复现：specs 含 `alpha`/`beta`/`gamma`/`delta` 四个 Required，调用
  `Parse(specs, nil)` 多次。
- 实际：错误均为 `ErrRequired` + nil Result（稳定），但消息里点名的标志随
  每次调用变化。实测 500 次分布：`alpha:321 beta:73 delta:53 gamma:53`，
  4 个名字都出现过（见 `TestMultipleMissingRequiredIsNondeterministic` 日志）。
- 应当：按 specs 声明顺序检查，确定性报告第一个缺失的 Required 标志（或聚合
  报告全部缺失），便于用户逐个补齐，也便于测试断言。
- 根因：`Parse` 末尾 `for _, s := range p.byLong` 遍历 map 检查 Required，
  Go map 迭代顺序每次随机，命中哪个缺失标志就报哪个。

## 备注

- 以上测试均断言**当前真实行为**（非期望行为），故全部通过；修复实现后需
  同步更新这些 characterization 测试。
- 未改动 `parse.go`/`spec.go`/`result.go` 的任何行为。
