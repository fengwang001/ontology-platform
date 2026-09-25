# FINDINGS：分组聚合器可求和边界两侧的实现/文档偏差

本文只记录**当前真实行为**（已由 `boundary_characterization_test.go`
钉住）与注释/文档承诺之间的不一致，不包含修复建议的代码改动。

## 发现 1：全部行不可求和的分组被折叠成「纯 int64 零和」

- **复现输入**
  ```go
  agg := NewAggregator([]string{"g"}, "v")
  agg.Add(map[string]any{"g": "a"})            // 缺失求和列
  agg.Add(map[string]any{"g": "a", "v": "x"})  // string
  agg.Add(map[string]any{"g": "a", "v": true}) // bool
  agg.Add(map[string]any{"g": "a", "v": math.NaN()})
  ```
- **实际输出**：`Count=4, Skipped=4, IsInt=true, IntSum=0, FloatSum=0`。
  单独一行缺失列 / `string` / `bool` / NaN 的分组也是同样形态
  （`Count=1, Skipped=1, IsInt=true, IntSum=0`）。
- **应当输出**：空和应能与真正的零和区分——例如不声称 `IsInt=true`
  （该组从未收到任何 int64），或提供独立的「无求和数据」标记。
  对照分组 `int64(5)` + `int64(-5)` 的输出是
  `Count=2, Skipped=0, IsInt=true, IntSum=0, FloatSum=0`，两组除
  `Count/Skipped` 外**逐字段完全相同**，`Sum()` 都返回 `0.0`。
- **根因分析**：`groupSum.result()`（`sum.go`）用单一布尔量
  `sawFloat` 区分两条分支：`if !s.sawFloat` 即注释所称「只有 int64
  值被加入」。但「没见到 float64」并不等于「见到过 int64」——全部行
  落入 skip 路径时 `rat` 从未被累加、保持零值，`big.Rat.Num()` 为
  `int64(0)`，于是 `IsInt64()` 通过，返回 `isInt:true, intSum:0`。
  状态机缺少「是否见过任何可求和值」这一维度，`skipped` 只进计数、
  不参与 `result()` 判定。下游目前只能用 `Skipped == Count` 这一
  隐含谓词间接推断「无数据」，而该约定在任何注释/README 中都未写明。
- **文档偏差**：
  - `sum.go` 的 `result()` 注释「Only int64 values were added」与
    `sumResult.isInt` 注释「true when only int64 values were summed」
    在零个可求和值时均为假命题（没有任何 value 被 summed）。
  - `agg.go` 的 `GroupResult` / `Sum()` 文档声称「需要精确整数的
    调用方先检查 IsInt」，但未说明 `IsInt=true` 也可能表示「无
    求和数据」，调用方据此无法区分 0 与空。

## 发现 2：非 int64 整数类型被静默跳过，无转换、无报错

- **复现输入**
  ```go
  agg.Add(map[string]any{"g": "a", "v": 5})        // 裸字面量 → int
  agg.Add(map[string]any{"g": "a", "v": int(5))
  agg.Add(map[string]any{"g": "a", "v": uint(5))
  agg.Add(map[string]any{"g": "a", "v": int32(5))
  agg.Add(map[string]any{"g": "a", "v": uint64(5))
  ```
- **实际输出**：上述每一种在单组单行情形下均为
  `Count=1, Skipped=1, IsInt=true, IntSum=0, FloatSum=0`——与传入
  `"x"`、`true` 或缺失列**完全相同**，不报错、不转换。只有 `int64(5)`
  得到 `Skipped=0, IntSum=5`。
- **应当输出**：行为本身（转换 / 报错 / 跳过）需要设计决策，但无论
  选哪种都应与文档一致；至少 `Add(map[string]any{"v": 5})` 这种最
  自然的调用点不应在零提示下丢数据。
- **根因分析**：`addValue`（`sum.go`）的类型分派是
  `switch n := v.(type) { case int64: ...; case float64: ...; default:
  s.skipped++ }`。Go 中非常量上下文的整数字面量默认类型是 `int`，
  `map[string]any{"v": 5}` 存入的动态类型也是 `int`，不匹配 `int64`
  分支，直接落 `default`。`int/uint/int32/uint64` 等全部同理。
  注释「Only int64 and float64 are summable; anything else ... is
  skipped」描述了代码事实，但未点明「常见整数类型按错误类型处理」
  这一陷阱，`skipped` 字段注释把它们与 NaN/缺失列笼统并列为
  「wrong type」，无法提示用户数据其实可无损转换。
- **叠加效应**：一个只含 `int` 行的分组同时触发发现 1——它不仅被
  跳过，还在 `Snapshot` 中自称 `IsInt=true, IntSum=0`，看起来像
  「求和成功且和为零」，是两处缺陷最隐蔽的组合形态。

## 文档/注释一致性清单

| 位置 | 承诺/措辞 | 实际 |
| --- | --- | --- |
| `sum.go` `result()` 注释 | 「Only int64 values were added」 | 0 个可求和值时也走此分支 |
| `sum.go` `sumResult.isInt` | 「true when only int64 values were summed」 | 全部跳过时为 true |
| `sum.go` `addValue` 注释 | 「Only int64 and float64 are summable」 | 字面成立，但未声明 `int` 等整数落入 skip |
| `agg.go` `GroupResult.Sum()` | 「need the exact integer should check IsInt」 | IsInt 无法区分零和与无数据 |
| `README.md` 特性列表 | 描述 Count/Sum 语义 | 未提及空和折叠与整数类型限制 |
