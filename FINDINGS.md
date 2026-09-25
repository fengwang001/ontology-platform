# 三值逻辑求值器：实现行为与文档承诺不一致清单

下列各条均为**当前真实行为**（已被 `characterization_test.go` 钉住，测试全部通过），
与注释/文档承诺存在偏差。本文档只记录，不修改实现。

## 1. `LeafCount` 不数 `KindIsNull` 叶子

- 位置：`evaluator.go:57`（仅 `KindCompare` 分支执行 `r.leaves++`），`evaluator.go:64`。
- 文档承诺：`evaluator.go:23` 注释为「number of leaf comparisons actually performed」。
  `predicate.go:45` 把 `KindIsNull` 定义为叶子节点。
- 复现输入：`AndP(IsNull("missing"), Eq("a", int64(1)))`，props `{"a":1}`。
- 实际输出：值 `True`，`LeafCount() == 1`；单独 `IsNull(...)` 计数恒为 `0`。
- 应当输出：若「leaf comparisons」涵盖所有叶子节点，应返回 `2`（单独 IsNull 返回 `1`）。
- 根因：计数写死在 Compare 分支，IsNull 分支既不计数也无注释说明其被排除，
  导致计数器实际含义是「Compare 叶子数」而非「叶子数」。

## 2. int64 经 float64 转换，超过 2^53 后精度丢失、错误判等

- 位置：`compare.go:14`（`float64(a)`）与 `compare.go:56`（字面量同样转 float64）。
- 文档承诺：`compare.go:45` 注释「int64 and float64 are mutually comparable」，
  未声明任何精度边界，暗示精确比较。
- 复现输入：
  - 属性 `int64(1<<53)`，字面量 `int64(1<<53+1)`，`Eq`；
  - 属性 `int64(1<<53)`，字面量 `float64(1<<53)+1.0`，`Eq`；
  - 属性 `int64(math.MaxInt64)`，字面量 `math.MaxInt64-1`，`Eq`。
- 实际输出：三者均为 `True`（不同整数被判等）；`Lt(1<<53, 1<<53+1)` 为 `False`。
- 应当输出：不同 int64 值的 `Eq` 应为 `False`、`Lt` 应为 `True`（精确跨类型比较，
  或至少在文档中明确「数值按 float64 语义比较，2^53 以上可能失真」）。
- 根因：两个操作数都先压成 IEEE-754 binary64；尾数只有 52 位，
  相邻大整数 round 到同一个 float64，比较在量化后的值上进行。

## 3. 空 `AndP()` / `OrP()` 被接受并返回恒等元

- 位置：`evaluator.go:88`（`evalAnd` 初值 `True`）、`evaluator.go:108`（`evalOr` 初值 `False`）；
  构造器 `predicate.go:55`、`predicate.go:60` 不校验子节点数。
- 文档承诺：`predicate.go:49` 写「KindAnd, KindOr: Children (at least one)」。
- 复现输入：`AndP()` 与 `OrP()`（以及嵌套 `NotP(AndP())`、`OrP(AndP())` 等），任意 props。
- 实际输出：`AndP()` → `True`，`OrP()` → `False`，无错误、`LeafCount() == 0`。
- 应当输出：二选一——要么构造期/求值期拒绝空集合并返回可判定错误，
  要么把文档改为「允许零子节点，返回合取/析取恒等元 True/False」。当前实现与注释互相矛盾。
- 根因：累加器初值即代数恒等元，循环零次直接返回；构造器没有 arity 强制。

## 4. `IsNull` 判定的是「键存在」，`nil` 值被视为非缺失

- 位置：`evaluator.go:64`（`_, ok := props[p.Attr]`）。
- 文档承诺：`predicate.go:46` 注释「tests whether an attribute is absent」，
  「absent」未区分「无键」与「值为 nil」。
- 复现输入：props `{"x": nil}`，求值 `IsNull("x")` 与 `Eq("x", int64(1))`。
- 实际输出：`IsNull("x")` → `False`（键在即非缺失）；同一 `nil` 值走 Compare 落入
  `compareLeaf` 的 `default` 分支，返回 `*TypeError`（reason `unsupported attribute type`）。
  仅当键完全不存在时 `IsNull` 才返回 `True`。
- 应当输出：若语义是「值缺失（含 nil）」，`{"x": nil}` 应返回 `True` 且后续 Compare
  应返回 `Unknown`；若语义就是「无键」，则应在注释中写明并定义 nil 值的比较行为。
- 根因：仅用 map 的 comma-ok 判键存在性，不解引用值；Compare 又无 `nil` 专用分支。

## 5. `KindNot` 子节点数不为 1 时返回普通 error，无法分类

- 位置：`evaluator.go:67`（`fmt.Errorf`），对比 `errors.go:24`、`errors.go:38` 的分类器。
- 文档承诺：`evaluator.go:40` 注释称 Eval 返回「a decidable error (*TypeError or *DepthError)」；
  `predicate.go:50` 写 Not 需「exactly one」。
- 复现输入：手动构造 `&Predicate{Kind: KindNot}`（0 子节点）或 2 子节点，任意 props。
- 实际输出：返回普通 `errors.errorString`；`IsTypeError(err)` 与 `IsDepthError(err)`
  均为 `false`，值为 `False`、计数 `0`。
- 应当输出：作为结构非法的谓词树，应返回 `*TypeError`（或新增可判定的结构错误类型），
  使其能被既有分类器识别；否则应放宽 Eval 注释中「只会返回两类可判定错误」的承诺。
- 根因：该分支直接用 `fmt.Errorf` 构造错误，未使用 `TypeError` 结构。

## 6. 深度检查先于叶子计数，边界叶子计入、超限叶子不计（行为正确，注释边界未钉死）

- 位置：`evaluator.go:52`（进入节点先判 `depth > maxDepth`），`evaluator.go:57` 计数在其后。
- 复现输入：`maxDepth=1` 求裸 `Eq`（depth 1）；外层包 1 个 `NotP` 后叶子位于 depth 2；
  `maxDepth=2` 求 `AndP(Eq, NotP(Eq))`。
- 实际输出：恰在 maxDepth 的 Compare 被允许且计数 `1`；再深一层立即返回
  `*DepthError`、`False`、计数不增加；先求值的兄弟叶子计数保留。IsNull 在任何深度均不计数。
- 应当输出：当前行为自洽，但 `NewEvaluator` 注释只说「deeper trees yield a *DepthError*」，
  没有钉死「根 depth=1、边界叶子计入、被拒叶子不计入」这三点；调用方若按 depth=0 心智
  模型使用，会对 `maxDepth=1` 仍允许一个叶子感到意外。
- 根因：非缺陷，属注释精度不足；characterization 测试将该顺序与边界固定以防回归。

## 汇总

| # | 主题 | 实际行为 | 偏差性质 |
|---|------|----------|----------|
| 1 | LeafCount | 只数 Compare，IsNull 永不计数 | 注释含义不清 |
| 2 | int64↔float64 | 2^53 以上失真、错误判等 | 注释承诺过强 |
| 3 | 空 And/Or | 接受，返回 True/False | 与「at least one」矛盾 |
| 4 | IsNull(nil) | 键在即 False；nil 比较报 TypeError | 语义未定义 |
| 5 | Not arity | 普通 error，两类分类器均 false | 与「decidable error」矛盾 |
| 6 | 深度边界 | 边界叶子计入、超限不计 | 行为正确，注释待补 |
