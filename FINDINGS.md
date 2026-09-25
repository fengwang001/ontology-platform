# FINDINGS：确定性重试调度器实现行为与注释/文档承诺的不一致

以下每条均由 `retry/characterization_test.go` 中的表驱动测试钉住（测试断言的是
**当前真实行为**，全部通过）。实现文件未做任何改动。

## 1. `baseDelay` 溢出饱和检测存在盲区：回绕到 `>= d` 时不饱和

- **位置**：`retry/policy.go` `baseDelay`，`next := d * Factor; if next < d { d = MaxInt64 } }`，注释仅写 `// overflow: saturate`。
- **复现输入**：`Policy{Base: 1<<62, Factor: 5}`，调用 `baseDelay(2)`。
- **实际输出**：`4611686018427387904`（= 2^62 = Base 本身），且 `baseDelay(k)` 对任意 k 都
  停在 2^62——退避完全不增长，也不饱和。
- **应当输出**：`9223372036854775807`（MaxInt64）。真实乘积 5 * 2^62 = 2^64 + 2^62 已溢出
  int64，按注释承诺应饱和。
- **对照**：`Factor = 2/3/4/6/8` 时乘积回绕为负数或 0，`next < d` 成立，正确饱和；
  `Factor = 5/9/13`（≡ 1 mod 4）时乘积恰好回绕成 `d` 本身，`next < d` 为假，饱和失效。
  更一般的形态：`Base = 2^62 + 2^59, Factor = 5` 回绕到 `7493989779944505344`，严格大于
  `d`，同样逃过检测，返回一个无意义的"增长"值。
- **根因分析**：`next < d` 只能识别"回绕到负数/小于 d"的溢出。int64 乘法回绕是模 2^64
  运算，结果落在 `[d, MaxInt64]` 区间时检测失效。正确的溢出判定应使用
  `math/bits.Mul64` 检查高位，或在乘前判断 `d > MaxInt64/Factor`。
- **附带误报**：同一判断对负 `Base` 误判——`Base = -100, Factor = 2` 时 `next = -200 < d`
  被当作"溢出"，结果饱和**向上**跳到 MaxInt64（`normalized` 不校验 `Base`，见第 4 条）。

## 2. `delay` 经 float64 中转：d > 2^53 时精度丢失，越界转换行为依赖平台

- **位置**：`retry/policy.go` `delay`，`return time.Duration(float64(d) * factor)`。
- **复现输入 A（精度）**：`Policy{Base: 1<<53 + 1, JitterPct: 100}`，`rnd = 0.5`
  （此时 `factor` 恰好 = 1.0，抖动为零）。
- **实际输出**：`9007199254740992`（= 2^53），比输入少 1——即使抖动因子精确为 1，
  结果也不等于 baseDelay。
- **应当输出**：`9007199254740993`（= 输入本身）。factor = 1 时 delay 应恒等于 baseDelay。
- **复现输入 B（向上舍入）**：`Base = MaxInt64 - 100`，`rnd = 0.5`：实际输出 MaxInt64，
  比输入**大** 100 ns（float64 向上舍入到 2^63，再转回）。
- **复现输入 C（越界转换）**：`Base = MaxInt64`，`rnd = 0.75`（factor = 1.5）：
  `float64(MaxInt64) * 1.5 ≈ 1.4e19` 超出 int64 范围，float64→Duration 转换在 Go 规范中
  是 implementation-defined；本平台（amd64）饱和为 MaxInt64，其他平台不保证。
- **根因分析**：float64 尾数仅 53 位，`d > 2^53` 时 `float64(d)` 不可逆；且转换前没有
  对 `float64(d) * factor >= 2^63` 做显式钳制。注释只说 "applying bounded jitter"，未
  提及大数值下的精度/溢出语义。安全做法：factor >= 1 时先判 `d > MaxInt64/factor`
   saturate，或用整数运算拆分 jitter（如 `d ± d*rand%pct`）。

## 3. `delay` 中 `if factor < 0 { factor = 0 }` 是不可达死代码

- **位置**：`retry/policy.go` `delay`。
- **复现输入**：任意合法组合 `rnd ∈ [0,1)`、`JitterPct ∈ [0,100]`（测试用
  6 × 5 网格遍历，含端点 `rnd = 0`、`JitterPct = 100`）。
- **实际输出**：`delay` 恒 >= 0；`factor` 的最小值为 `1 - j >= 0`（在 `rnd = 0, j = 1`
  处取到 0），`factor < 0` 永不成立，守卫分支从未执行。
- **应当输出**：行为本身正确，问题在于代码与注释暗示存在"factor 可能为负需要下限保护"
  的情形，误导读者以为该分支有作用。
- **根因分析**：`factor = 1 + (2*rnd-1)*j`，`2*rnd-1 ∈ [-1,1)`，`j ∈ [0,1]`，故
  `factor ∈ [0, 2)`，数学上不可能为负。该守卫唯一"可达"的前提是 `normalized` 未 clamp
  `JitterPct > 100`——但 clamp 存在（第 4 条），两条防御逻辑互相掩盖，任一条改动都会
  让另一条的死代码/静默行为暴露。应删除死分支或改为 `factor = max(factor, 0)` 的显式
  不变式注释。

## 4. `normalized` 静默 clamp 非法值，调用方无法区分"非法输入"与"默认值"

- **位置**：`retry/policy.go` `normalized`；文档仅 `Policy` 字段注释里各一句
  "Values <= 0 are treated as 1" 等。
- **复现输入与实际输出**（测试逐项钉住）：
  - `MaxAttempts = -3` → clamp 为 1：`Do` 只试 1 次、零等待、返回 `ErrExhausted`；
  - `Factor = -2` → clamp 为 1：退避退化为恒定 `Base`（无增长）；
  - `JitterPct = -5` → clamp 为 0：抖动静默关闭，`rnd` 一次都不会被调用；
  - `JitterPct = 250` → clamp 为 100：`rnd = 0.2` 时 delay = 400（若未 clamp，
    factor = -0.5 会落入第 3 条的死守卫而变成 0）；
  - `Base`/`Cap` **不** clamp：`Base = -100, Cap = -5` 原样透传（负 `Base` 随后触发
    第 1 条的误判饱和）。
- **应当输出**：行为本身自洽，但承诺不一致——四个字段的非法值处理是"静默改写"，既无
  错误返回也无日志，且 `Base`/`Cap` 连 clamp 都没有，校验口径不统一。
- **根因分析**：`normalized()` 返回 `Policy` 值而非 `(Policy, error)`，非法输入与合法
  默认值在归一化后不可区分；调用方传错参数（如 `JitterPct: -5` 本想关闭抖动、
  `Factor: -2` 本想报错）时得到的是静默降级后的调度，故障定位困难。若要保持兼容，
  至少应在文档中明确"非法值被静默 clamp 且 Base/Cap 不校验"的完整语义。

## 附：测试与验证

- 新增 `retry/characterization_test.go`（唯一新增/修改的 .go 文件），4 个表驱动测试：
  `TestBaseDelayOverflowSaturationBlindSpot`、`TestDelayFloat64PrecisionLoss`、
  `TestDelayFactorNeverNegative`、`TestNormalizedSilentClamp`。
- 验证：`go test -race ./...` 全部通过，`go vet` 与 `gofmt` 干净。
- 注意：第 2 条中越界 float64→int64 转换的结果（饱和到 MaxInt64）是平台相关行为，
  对应断言钉的是本仓库当前运行平台（linux/amd64）的真实结果。
