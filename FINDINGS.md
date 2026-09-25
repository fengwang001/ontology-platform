# 确定性重试调度器：实现行为与文档/注释承诺不一致清单

以下每条均由 `retry/characterization_test.go` 中的 characterization tests
钉住「当前真实行为」（测试全部通过）。本次未改动任何实现文件。

## 1. baseDelay 溢出饱和检测漏掉「回绕后仍 ≥ d」的情形

- 位置：`retry/policy.go` `baseDelay`，`next := d * Factor; if next < d`。
- 复现输入：`Policy{Base: 1<<62 /* 2^62 */, Factor: 5}`，调用 `baseDelay(2)`。
- 实际输出：`4611686018427387904`（即 `2^62`，等于 Base，未饱和）。
- 应当输出：`math.MaxInt64`（`9223372036854775807`）。数学上
  `5 * 2^62 = 2^64 + 2^62` 已超过 int64 上界。
- 根因：int64 乘法按二进制补码回绕，`5*2^62 mod 2^64 = 2^62`，回绕结果
  恰好等于 `d`，于是 `next < d` 为假、饱和分支不触发。该守卫只挡住回绕到
  `(-∞, d)` 的情形。对照 Factor=2/3/4 回绕成 `-2^63`/`-2^62`/`0`（均 `< d`）
  能正确饱和；Factor=5 漏网。且 d 此后固定在 `2^62`，每轮再次回绕到自身，
  `baseDelay(k)` 对所有 k≥2 都冻结在 Base 而非 MaxInt64。

## 2. delay 经 float64 中转导致大 Duration 精度丢失/溢出行为依赖平台

- 位置：`retry/policy.go` `delay`，`time.Duration(float64(d) * factor)`。
- 复现输入（两类）：
  1. 精度丢失：`Policy{Base: 1<<62 + 1, Factor: 1, JitterPct: 100}`，
     `rnd=0.5`（factor=1）。
  2. 溢出：`Policy{Base: math.MaxInt64, Factor: 1, JitterPct: 100}`，
     `rnd=math.Nextafter(1,0)`（factor≈1.9999999999999998，乘积 ≈ 1.84e19）。
- 实际输出：
  1. `4611686018427387904`（`2^62`），精确值应为 `4611686018427387905`
     （Base 本身），差 1ns；`float64(2^62+1)` 被舍入成 `2^62`。
  2. 在本工具链（linux/arm64, go1.26.5）上为 `math.MaxInt64`（饱和）。
- 应当输出：按 Duration 整数语义计算的精确值；溢出时按文档约定一致地饱和
  到 MaxInt64（而非依赖 float→int 转换的实现定义结果——Go 语言规范对
  超出 int64 范围的浮点转换结果是 implementation-specific，其他架构可能
  得到 MinInt64 等不同值）。
- 根因：`d > 2^53` 时 float64 无法逐位表示整数；`float64(d)*factor` 的
  舍入误差在转回 Duration 后不可恢复，且 float 乘积无上界保护。

## 3. `if factor < 0 { factor = 0 }` 是死代码

- 位置：`retry/policy.go` `delay`。
- 复现/验证：遍历所有合法 `JitterPct ∈ [0,100]`（clamp 后）与契约内
  `rnd ∈ [0,1)`（含 0 与最接近 1 的值），
  `factor = 1 + (2*rnd-1)*(JitterPct/100)` 恒 ≥ 0（最小值在 rnd=0、
  JitterPct=100 时取 0），下限分支对任何合法输入都不可达。
- 实际输出：该分支从不执行；合法输入下 delay 恒等于
  `time.Duration(float64(baseDelay) * factor)`。
- 应当输出：删除该死分支及其暗示「会出现负 factor」的注释；或如果要防御
  契约外 rnd，则应在入口校验 rnd 范围而不是静默夹取。
- 根因：注释「factor < 0 下限」暗示有保护作用，但 `2*rnd-1 ∈ [-1,1)`、
  `j ≤ 1` 数学上保证 factor ≥ 0。仅当传入违反契约的 rnd（如 -1）时分支才
  触发（此时 delay 被夹成 0），而该来源本不在 `New` 文档承诺范围内。

## 4. normalized() 对非法配置静默 clamp，调用方无法区分非法与默认

- 位置：`retry/policy.go` `normalized`（由 `New` 无条件调用）。
- 复现输入与实际（被钉住的）行为：
  - `MaxAttempts = 0 / -7` → 静默改为 1；`Do` 只跑 1 次、无等待、返回
    `ErrExhausted`。
  - `Factor = 0 / -9` → 静默改为 1；`Do` 的退避序列平坦（不增长）。
  - `JitterPct = -40` → 静默改为 0；`Do` 完全不调用 rnd，delay 等于 Base。
  - `JitterPct = 150` → 静默改为 100；`Do` 的抖动带被放大为 [0, 2*Base]
    （rnd=0 时等待 0），调用方无法得知配置被拒。
- 应当输出：`New`/校验返回明确错误（或至少提供非静默的校验入口），让调用
  方能区分「用户传了非法值」与「有意使用默认值」。
- 根因：注释仅写「Values <= 0 are treated as 1」「[0,100]」，把输入校验
  实现成无返回值的就地改写；非法值被吞掉，错误配置（尤其是 JitterPct
  超 100 导致抖动范围扩大）不会暴露。

## 备注

- `Do` 的尝试/等待/终止语义（首试立即、等待数 = 尝试数-1、末次失败后不
  等待、`Permanent` → `ErrAborted`、耗尽 → `ErrExhausted`）与文档一致，
  既有测试已覆盖，本次未发现偏差。
- 以上均为「加测试钉行为」，未修复；修复建议（供后续）：baseDelay 用
  `d > MaxInt64/Factor` 预判溢出；delay 用整数运算（先按
  numerator/denominator 比例计算并 clamp 到 [0, MaxInt64]）替代 float64
  中转；删除死分支或改为 rnd 契约校验；normalized 改为返回 error。
