# Coercion 数值边界问题清单

本文记录 `scalar.go` 当前真实行为与注释/文档承诺不一致之处。
所有结论均由 `boundary_characterization_test.go` 钉住；本次未改动任何实现文件。

约定：Strict = 报错返回；Lenient = 降级为 `Degradation` 记录。

## 1. float64→int64：`-0.0` 与 `+0.0` 报错不对称

- 复现输入：`math.Copysign(0, -1)` → `Int64Kind`，对照 `0.0` → `Int64Kind`。
- 实际输出：
  - `-0.0`：Strict 返回 `(0, PrecisionLoss)`，detail「negative zero sign discarded;
    discarded fraction 0」；Lenient 值为 0 并记录 1 条 `PrecisionLoss`。
  - `+0.0`：两种模式均返回 `(0, nil)`，无任何记录。
- 应当输出：二者数值完全相等（`-0.0 == 0.0`），转换结果同为 0，应要么都不报错，
  要么按同一规则处理符号位；不应只因 signbit 不同而一条报错一条静默。
- 根因：`floatToInt64` 中存在特判 `math.Signbit(number) && number == 0`，
  该检查仅在负零时触发，detail 还附带了与负零无关的「discarded fraction 0」。

## 2. float64→int64：2^53+1 因 float64 舍入逃过 PrecisionLoss

- 复现输入：字面量 `9007199254740993.0`（数学上的 2^53+1）→ `Int64Kind`。
- 实际输出：无错误、无降级，值为 `9007199254740992`。
  作为对照，`math.Nextafter(2^53, +Inf)`（= 2^53+2）正常报 `PrecisionLoss`；
  `2^53` 本身也正确地不报错。
- 应当输出：注释承诺「magnitude exceeds exact float64 integer range 2^53」，
  即严格大于 2^53 的整数应报 `PrecisionLoss`。但 2^53+1 在源语言层面无法表达：
  无类型常量存入 `float64` 时即被舍入为 2^53（bits `0x4340000000000000`），
  运行时无法再区分。
- 根因：`magnitudeExceedsExactIntegerRange` 用 `absolute == 2^53` 判「恰好边界不越界」，
  对已经舍入到 2^53 的值当然成立而返回 false。基于运行时 float64 的判据无法捕捉
  「2^53+1」这个语义输入——阈值检查与 float64 表示能力之间的陷阱未被注释说明，
  容易让人以为传入 2^53+1 会报错。

## 3. int64→float64 与 float64→int64 的精确阈值相差 1

- 复现输入：`int64(9007199254740992)`（2^53）→ `Float64Kind`，
  对照 `int64(9007199254740991)`（2^53−1）；负数侧 `-2^53` 同理。
- 实际输出：`int64(2^53)` 报 `PrecisionLoss`（detail「integer ... is not exactly
  representable as float64」），而 `float64(2^53)` → int64 在第 2 条中明确不越界；
  `2^53−1` 两侧均无损。
- 应当输出：2^53 是 float64 可精确表示的整数（IEEE-754 在该量级 ULP 为 2，
  2^53 恰为格点），转换无损，不应报 `PrecisionLoss`。真正首个非格点奇数是 2^53+1
  （int64 中首个可表达且有损的是 2^53+2，因为 2^53+1 会舍入到 2^53）。
- 根因：`toFloat64` 阈值写成 `number < -9007199254740991 || number > 9007199254740991`
  （2^53−1），而 `magnitudeExceedsExactIntegerRange` 以 2^53 为边界。
  两处对「精确整数范围」的定义相差 1，方向相反且都不完整。

## 4. 溢出整数文本返回 0，丢弃 strconv 的夹取值

- 复现输入：`"9223372036854775808"`（MaxInt64+1）与
  `"-9223372036854775809"`（MinInt64−1）→ `Int64Kind`。
- 实际输出：Strict 与 Lenient 的值均为 `0`，类别 `Overflow`；
  `Error.Converted`（及对应 `Degradation.Converted`）为 `nil`，未携带任何降级值。
- 应当输出：与 float64 溢出路径保持一致——float 超界时返回夹取的
  `MaxInt64`/`MinInt64` 并把夹取值放入 `Converted`。文本路径同样应返回夹取值
  （`strconv.ParseInt` 在 `ErrRange` 时已经给出 `MaxInt64`/`MinInt64`），
  类别仍为 `Overflow`。
- 根因：`toInt64` 检测到 `isRangeError` 后直接 `return 0`，把 `parsed`
  （ParseInt 夹取后的结果）丢弃，且构造 `Error` 时未设置 `Converted`。
  注释「Overflow means a value exceeded the target numeric range」与
  result.go 的「degraded result, when available」均未说明此处刻意归零。

## 5. toBool：数值侧严格 0/1，文本侧宽容 trim/大小写

- 复现输入：`float64(0.5)`、`int64(2)`、`float64(2)` → `BoolKind`（拒绝侧）；
  `" TRUE "`、`"1"`、`"\tfalse\n"` → `BoolKind`（接受侧）。
- 实际输出：
  - `0.5` 与 `2`（int64/float64）：返回 `false` + `Invalid`；Lenient 也保留该错误
    （`skipped` 语义），不产生 Degradation。
  - `" TRUE "` → `true`、`"1"` → `true`、`"\tfalse\n"` → `false`，均无错误。
- 应当输出：注释称「accepted values are exactly true, false, 1, 0, or their text
  forms」，暗示两侧口径一致。实际数值侧要求位级恰好 0/1（0.5 被当作非法而非
  截断/四舍五入），文本侧却先 `TrimSpace` 再 `ToLower`。两侧对「等价形态」的
  宽容度不对称，且注释没有点明数值侧是精确匹配。
- 根因：`toBool` 的 `int64`/`float64` 分支用 `typed == 0 || typed == 1` 精确比较，
  string 分支用 `strings.ToLower(strings.TrimSpace(...))` 归一化后匹配；
  两条路径没有共享同一套接受规则。

## 验证方式

- `go test ./...`：全部通过（含新增 characterization tests）。
- `go vet ./...`、`gofmt -l .`：干净。
- 复现入口：`boundary_characterization_test.go` 中的
  `TestCharacterize*` 五个表驱动测试；边界值通过循环遍历，未逐值展开。
