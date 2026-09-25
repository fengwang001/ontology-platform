# FINDINGS: coercion 数值边界行为与注释/文档的不一致

以下每条均为**当前实现的真实行为**（已由 `boundary_characterization_test.go`
钉住），并与实现注释或语义承诺对照。本文档只记录不一致，不修改实现。

涉及代码：`scalar.go` 的 `floatToInt64`（检查顺序：NaN/Inf 拒绝 → int64
范围夹取 → 负零符号 → 小数截断 → 2^53 精确整数范围）、`toFloat64`、
`toInt64`、`toBool`。

## 1. 负零与正零不对称：同一数值 0 只因符号位报错

- 复现输入：`Convert(math.Copysign(0, -1), Int64Kind)` 对照 `Convert(0.0, Int64Kind)`
- 实际输出：`-0.0` 报一条 `PrecisionLoss`（detail「negative zero sign
  discarded; discarded fraction 0」）并返回 0；`+0.0` 不报任何错误、同样返回 0。
- 应当输出：两者行为一致。要么都认为「int64 无法表示符号位、零的符号被丢弃」
  属于有损（都报），要么都认为数值 0 无信息丢失（都不报）；同一数学值 0
  不应因符号位不同而一条报错一条静默。
- 根因：`floatToInt64` 中 `math.Signbit(number) && number == 0` 只对负零
  特判并提前 return，正零绕过该分支直接落到正常路径。该检查位于小数截断
  检查之前，且注释「non-finite float cannot become int64」等均未提及负零
  这一特殊口径。

## 2. 2^53 舍入陷阱：字面量 2^53+1 永远触发不了 PrecisionLoss

- 复现输入：`Convert(9007199254740993.0, Int64Kind)`（意图传入 2^53+1）
- 实际输出：无错误，返回 `int64(9007199254740992)`。因为 float64 无法精确
  表示 2^53+1，字面量在编译期就被舍入成 2^53，运行时检查看到的已经是
  2^53。边界实际行为：`|x| <= 2^53` 不报，`|x| >= 2^53+2` 才报
  `PrecisionLoss`（detail「magnitude exceeds exact float64 integer range
  2^53」）。
- 应当输出：语义上「超过 2^53 精确整数范围」的值应报 `PrecisionLoss`；
  但调用方写下的 2^53+1 在到达检查前已丢失，检查无法区分「用户写了
  2^53」与「用户写了 2^53+1 被舍入」。该陷阱应至少在注释中说明。
- 根因：`magnitudeExceedsExactIntegerRange` 用 `absolute ==
  9007199254740992.0` 判「恰好 2^53 不越界」，判断发生在 float64 舍入
  之后；2^53+1 这个数学值在 float64 域内根本不存在，检查对它不可达。

## 3. int64→float64 与 float64→int64 阈值相差 1：2^53 被误报

- 复现输入：`Convert(int64(9007199254740992), Float64Kind)`（对照
  `int64(9007199254740991)`）
- 实际输出：`int64(2^53)` 报 `PrecisionLoss`（detail「integer
  9007199254740992 is not exactly representable as float64」），但 2^53
  恰好可以精确表示为 float64（它是 2 的幂）；`int64(2^53-1)` 不报。
  而反方向 `float64(2^53) → int64` 按第 2 条的边界是**不报**的。
- 应当输出：`int64(2^53)` 转换无损，不应报 `PrecisionLoss`；两个方向的
  「精确表示」阈值应一致（都为 2^53 或都为 2^53−1）。
- 根因：`toFloat64` 的阈值写成 `number < -9007199254740991 || number >
  9007199254740991`（即 2^53−1），而 `magnitudeExceedsExactIntegerRange`
  用 2^53。两处对「float64 精确整数上界」的取值差 1，且 detail 文案
  「not exactly representable」对 2^53 是事实性错误。

## 4. 溢出文本丢弃 ParseInt 的夹取值，返回 0

- 复现输入：`Convert("9223372036854775808", Int64Kind)`（MaxInt64+1）
- 实际输出：类别为 `Overflow`，但返回值是 `int64(0)`；Lenient 模式下
  degradation 的 `Converted` 字段为 `nil`。`strconv.ParseInt` 在返回
  `ErrRange` 时同时返回夹取值（MaxInt64/MinInt64），该值被直接丢弃。
  对照同文件 float64 溢出路徑：`1e300 → Int64` 报 `Overflow` 并返回
  夹取值 `math.MaxInt64`，`Converted` 也有记录——两条溢出路径口径相反。
- 应当输出：与 float64 溢出路径一致，返回夹取值
  （`math.MaxInt64`/`math.MinInt64`）并把它记入 `Converted`，让
  「Overflow + 夹取降级结果」的契约对文本与浮点输入一致。
- 根因：`toInt64` 的 `isRangeError` 分支只取了 `err`，忽略了
  `strconv.ParseInt` 同步返回的夹取值，`report` 时未设置 `Converted`，
  随后 `return 0`。

## 5. toBool 数值侧严格 0/1、文本侧宽容 trim/大小写

- 复现输入：`Convert(0.5, BoolKind)`、`Convert(int64(2), BoolKind)` 对照
  `Convert(" TRUE ", BoolKind)`、`Convert("1", BoolKind)`
- 实际输出：数值侧只认**恰好** 0 或 1（`float64(0.0)`/`float64(1.0)`/
  `int64(0)`/`int64(1)` 通过；`0.5`、`2`、`-1` 落 default 报 `Invalid`）；
  文本侧却接受 trim 空白且大小写不敏感的
  `"true"/"false"/"1"/"0"`（`" TRUE "`、`"\tFalse\n"` 均通过）。
- 应当输出：两侧口径应有一致性说明。若设计意图是「文本来自用户输入故
  宽容、数值来自程序故严格」，注释应写明；否则 `" 1 "` 可进而 `1.0`
  之外的数值一律拒绝，属于同类输入的不对称对待。
- 根因：`toBool` 的 `string` 分支做了
  `strings.ToLower(strings.TrimSpace(...))`，而 `int64`/`float64` 分支
  只做 `== 0 || == 1` 的精确比较；注释「accepted values are exactly
  true, false, 1, 0, or their text forms」未区分两侧的宽容度差异。

## 附：检查顺序备忘（floatToInt64）

1. NaN/Inf → `Invalid`，返回 0；
2. 超出 int64 范围 → `Overflow`，返回夹取值（MaxInt64/MinInt64）；
3. 负零 → `PrecisionLoss`，返回 0（见第 1 条）；
4. 小数部分非零 → `PrecisionLoss`，继续；
5. 绝对值 > 2^53 → `PrecisionLoss`，继续（见第 2 条）。

注意第 4、5 条实际上互斥：float64 在 |x| >= 2^52 后 spacing >= 1，
小数部分必为零，故「有小数」与「超过 2^53」不会同时触发；第 3 条在
第 4 条之前 return，负零永远不会走到小数检查。
