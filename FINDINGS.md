# 分组聚合器边界行为排查

本文件记录 `Aggregator`（`agg.go` / `sum.go`）在「可求和值」边界两侧的
实际行为与文档/注释承诺不一致之处。配套 characterization tests 见
`characterization_test.go`，它们钉住下述**当前真实行为**，全部通过；
本次未改动任何实现文件。

## 发现 1：全不可求和分组被折叠成「纯 int64、和为 0」

- **复现输入**：分组只有一行，且该行求和列缺失 / 为 nil / `string` /
  `bool` / `NaN`（或这些形态的任意组合），例如
  `NewAggregator([]string{"g"}, "v"); Add({"g":"a"})`。
- **实际输出**：`GroupResult{Count:1, Skipped:1, IsInt:true,
  IntSum:0, FloatSum:0}`；`Sum()` 返回 `0`。多行全跳过时
  `Count:Skipped` 同步增大，求和字段仍全部为零、`IsInt` 仍为 `true`。
- **应当输出**：该分组没有任何可求和值参与累加，不应被宣称为
  「全 int64 分组」。需要一个可区分「无数据」的信号（例如
  `IsInt=false` 且 `FloatSum` 无效、新增 `HasSum bool`，或在
  `Count==Skipped` 时由调用方显式判定），而不是与真实零和逐字段相同。
- **根因分析**：`groupSum` 只用布尔位 `sawFloat` 区分两种状态。
  `result()` 的判据是 `if !s.sawFloat { 纯 int64 分支 }`
  （`sum.go`），而「从未见到可求和值」与「只见到 int64」在该布尔位上
  完全无法区分。空 `big.Rat` 的 `Num()` 恰好是 0 且 `IsInt64()` 为真，
  于是空和被读成 `int64(0)`。注释「Only int64 values were added, so the
  rational is an integer」隐含假设「至少有值被加入」，该假设无任何字段
  支撑。

## 发现 2：真实零和与无数据在求和字段上逐字段相同

- **复现输入**：A 组 `int64(5)` + `int64(-5)`；B 组两行全不可求和
  （如缺失列 + 字符串）。
- **实际输出**：两组 `IsInt=true, IntSum=0, FloatSum=0, Sum()=0`
  完全相同；只有 `Count`/`Skipped` 不同（A 为 2/0，B 为 2/2）。
  当行数也相同（如 A 用一个 `int64(0)`，B 用一个字符串）时，**唯一**
  能区分两组的公开字段是 `Skipped`。
- **应当输出**：「精确和为零」与「没有可求和值」是不同的聚合事实，
  下游应能用专门字段（而非反推 `Count == Skipped`）区分；当前公开 API
  未承诺、文档也未说明需要靠 `Skipped` 侧信道判定。
- **根因分析**：发现 1 的直接后果。`GroupResult` 无「是否有值入和」
  字段，`IsInt` 语义实际是「未见过 float64」而非「见过至少一个
  int64」；`Snapshot()` 只是原样透传该误判（`agg.go`）。

## 发现 3：非 int64 整数类型被静默跳过，且无错误

- **复现输入**：`Add({"g":"a","v":5})`、`int(5)`、`uint(5)`、
  `int32(5)`（分别装箱进 `any`）。
- **实际输出**：四种输入全部落 `default`：`Skipped=1`、
  `IsInt=true`、`IntSum=0`，不报错、不转换。其中 `5` 作为无类型整数字
  面量进入 `map[string]any` 时默认类型是 `int`，即最自然的写法
  `Add({"v": 5})` 求和被静默丢弃。对照组 `int64(5)` 正常求和为 5、
  `Skipped=0`。
- **应当输出**：三选一且应在注释/文档中写明：精确转换进有理累加器
  （`int`/`uint`/`int32` 可经 `big.Int.SetInt64` 或 `SetUint64` 安全
  纳入，超界再走 overflow）；或在运行时返回明确错误；绝不应静默计入
  `Skipped` 且无任何告警。当前行为与 `README`/类型名「Sum 聚合」的
  直觉相悖。
- **根因分析**：`addValue` 的类型开关只有 `case int64` 与
  `case float64` 两个分支（`sum.go`），Go 的类型 switch 不做隐式类型
  转换，命名整数类型与 `int64` 不是同一动态类型，故其余全部落入
  `default: s.skipped++`。注释「Only int64 and float64 are summable;
  anything else ... is skipped」描述了机制，但未说明非 int64 整数
  应转换还是报错，属语义缺口而非实现笔误。

## 边界已确认无额外异常的点

- NaN 单独成组时与其他不可求和形态一致（发现 1），且不会置位
  `sawFloat`，不触发「混合降级为 float」。
- 全不可求和分组不会触发 `*OverflowError`：空有理和读成 0，
  `IsInt64()` 为真，走正常返回路径。
- 分组键侧（`key.go`）不受影响：缺失/类型异常只发生在求和列，
  键列的 absent/nil/empty 区分与排序仍按既有承诺工作。
