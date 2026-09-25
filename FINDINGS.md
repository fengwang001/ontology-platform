# FINDINGS：实现行为与注释/文档承诺的不一致

本文档逐条记录三值逻辑谓词求值器中「实现行为」与「代码注释/文档承诺」不一致
之处。每条给出复现输入、实际输出、应当输出与根因分析。所有「实际输出」均已
由 `characterization_test.go` 中的表驱动测试钉住（当前实现下全部通过）。

## F1. int64 属性超过 2^53 时比较静默丢精度

- **承诺**：`compareNumeric` 注释称 "int64 and float64 are mutually
  comparable"，暗示 int64 比较是精确的；`Predicate` 的 `Eq/Lt/Gt` 构造器
  对数值比较无任何精度限制说明。
- **复现输入**：`props = {"a": int64(9007199254740993)}`（2^53+1），
  谓词 `Eq("a", int64(9007199254740992))`（2^53）。
- **实际输出**：`True`（两个不同的 int64 被判相等）；同理
  `Lt("a", 2^53+1)` 作用于 `a = 2^53` 返回 `False`，
  `Eq("a", math.MaxInt64-1)` 作用于 `a = math.MaxInt64` 返回 `True`。
- **应当输出**：`Eq` 为 `False`、`Lt` 为 `True`——按整数精确语义比较。
- **根因**：`compare.go` 中 `compareLeaf` 的 `case int64` 先执行
  `float64(a)`，`compareNumeric` 再把 int64 字面量也转成 `float64`。
  float64 只有 52 位尾数，2^53+1 舍入回 2^53，两个不同整数在转换后
  坍缩为同一浮点数，比较结果随之错误且无任何报错或 Unknown 信号。

## F2. 「存在但 nil」属性：IsNull 与叶子比较答案互相矛盾

- **承诺**：`IsNull` 注释称 "tests whether an attribute is absent"，
  即区分「不存在」与「存在」；`compareLeaf` 注释称 "Incomparable
  operand types yield a *TypeError"，nil 并非一种可比较类型之外的
  「不兼容类型对」，而是根本没有值。
- **复现输入**：`props = {"a": nil}`，分别求值 `IsNull("a")` 与
  `Eq("a", int64(1))`（`Lt`/`Gt` 同理）。
- **实际输出**：`IsNull` 返回 `False`（「不是 null」），而 `Eq/Lt/Gt`
  全部返回 `(False, *TypeError{Reason: "unsupported attribute type",
  AttrType: "<nil>"})`。同一状态，一条路径说「有值」，另一条路径说
  「类型不支持、无法求值」。
- **应当输出**：二者一致。合理语义是「存在但未赋值」按缺失处理：
  `IsNull` 返回 `True`，叶子比较返回 `Unknown`（与缺属性一致）；或至少
  比较返回 Unknown 而非 *TypeError。
- **根因**：`evaluator.go` 的 `KindIsNull` 分支只检查 map key 存在性
  （`_, ok := props[p.Attr]`），不看值是否为 nil；`compare.go` 的
  `compareLeaf` type switch 没有 `case nil`，nil 落入 default 分支被
  当作「unsupported attribute type」报错。两条路径各自实现，从未对齐
  nil 的语义。

## F3. 空 AndP()/OrP() 静默返回真空值，与文档「至少一个子节点」矛盾

- **承诺**：`predicate.go` 中 `Predicate` 的文档注释写明
  "KindAnd, KindOr: Children (at least one)"。
- **复现输入**：`AndP()`、`OrP()`（无子节点），任意 props。
- **实际输出**：`AndP()` 返回 `(True, nil)`，`OrP()` 返回
  `(False, nil)`，leaf 计数为 0，无任何错误。
- **应当输出**：按文档承诺，零子节点属于非法树，应返回错误（理想情况
  是类型化的、可被 `IsTypeError` 或专门谓词识别的错误）。
- **根因**：`evalAnd`/`evalOr` 用带初值的循环实现（`result := True` /
  `result := False`），零子节点时循环体不执行，直接返回真空真值；
  没有任何地方校验子节点数量。

## F4. NotP 元数错误是普通 error，无法被 IsTypeError/IsDepthError 识别

- **承诺**：`Evaluator.Eval` 注释称 "It returns the three-valued result
  or a decidable error (*TypeError or *DepthError)"，即所有可判定错误
  都是类型化的。
- **复现输入**：`&Predicate{Kind: KindNot}`（0 个子节点）或
  `&Predicate{Kind: KindNot, Children: []*Predicate{leaf, leaf}}`
  （2 个子节点）。
- **实际输出**：返回 `(False, error)`，error 为
  `fmt.Errorf("ontology: Not node requires exactly 1 child, got %d")`
  产生的普通错误；`IsTypeError(err)` 与 `IsDepthError(err)` 均为
  `false`，调用方无法用既有谓词分类该错误（`Kind` 未知的 default
  分支同样返回普通 error）。
- **应当输出**：错误应类型化（如专门的 `*ArityError` 或复用
  `*TypeError`），或文档应明确说明结构性错误不属于类型化错误。
- **根因**：`evaluator.go` 的 `KindNot` 分支与 default 分支直接使用
  `fmt.Errorf` 构造错误，绕过了 `TypeError`/`DepthError` 类型体系，
  而 `Eval` 的文档注释未给结构性错误留出演出口。

## F5. -0.0 与 +0.0 被判相等（行为确认，非缺陷）

- **复现输入**：`props = {"a": math.Copysign(0, -1)}`，谓词
  `Eq("a", float64(0))`。
- **实际输出**：`True`；`Lt`/`Gt` 均为 `False`。
- **说明**：这符合 IEEE 754 浮点比较语义（`-0.0 == +0.0`），与
  `cmpOrdered` 基于 `<`/`>` 的实现一致，本身不算缺陷；但实现与文档
  均未声明该语义，若下游依赖符号位区分（如 `math.Signbit`）会产生
  意外。此处仅以测试钉住行为，供后续语义决策参考。
