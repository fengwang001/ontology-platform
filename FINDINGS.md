# FINDINGS：三值逻辑求值器实现与文档承诺的偏差

以下每条均由 `characterization_test.go` 中的表驱动测试钉住（断言的是当前真实
行为，非期望行为）。源码位置对应本仓库当前实现。

## 1. int64 超过 2^53 时比较静默丢精度

- **复现输入**：`props = {"a": int64(9007199254740993)}`，
  谓词 `Eq("a", int64(9007199254740992))`（或 `float64(9007199254740992)`）。
- **实际输出**：`(True, nil)`——两个不同的 int64 被判相等；
  `Lt`/`Gt` 均返回 `False`。对比：`9007199254740993` 与 `9007199254740994`
  又不相等（前者舍入到 2^53，后者可精确表示），行为随舍入方向不规则。
- **应当输出**：`(False, nil)`（Eq），`Gt` 应为 `True`。`compareNumeric` 的
  注释只承诺 "int64 and float64 are mutually comparable"，未声明任何精度
  损失；对 >2^53 的 int64 该承诺不成立。
- **根因分析**：`compare.go` 中 `compareLeaf` 的 `case int64` 无条件执行
  `float64(a)`，`compareNumeric` 对 int64 字面量同样 `float64(l)`。float64
  只有 52 位尾数，|v| > 2^53 的整数按 round-half-to-even 舍入，不同的
  int64 可能映射到同一 float64，比较在舍入后的值上进行，精度静默丢失。
  测试：`TestInt64PrecisionLossAbove2Pow53`。

## 2. 「存在但 nil」属性在 IsNull 与叶子比较间语义矛盾

- **复现输入**：`props = {"a": nil}`，分别求值 `IsNull("a")` 与
  `Eq("a", int64(1))`（`Lt`/`Gt`、`Eq("a", nil)` 同理）。
- **实际输出**：`IsNull` 返回 `(False, nil)`（属性"非空"）；而所有叶子
  比较返回 `(False, *TypeError{AttrType: "<nil>", Reason: "unsupported
  attribute type"})`。同一状态，一条路径说"有值"，另一条说"类型不支持"。
  对照真正缺失的属性：`IsNull` 为 `True`，`Eq` 为 `(Unknown, nil)`。
- **应当输出**：两路径一致——"存在但 nil"应类比缺失属性（`IsNull` 为
  `True`、比较得 `Unknown`），或文档显式定义第三种状态；绝不应一边判
  "非空"一边抛类型错误。
- **根因分析**：`evaluator.go` 的 `KindIsNull` 分支只看
  `_, ok := props[p.Attr]` 的存在位，不检查值是否为 nil；`compare.go`
  的 `compareLeaf` 类型 switch 没有 `case nil`，nil 落入 `default` 分支
  被打成 `*TypeError`。两条路径对 nil 的建模从未对齐。
  测试：`TestNilAttrSemantics`。

## 3. 空 AndP()/OrP() 不报错，静默返回单位元

- **复现输入**：`AndP()` 与 `OrP()`（无子节点），任意 props。
- **实际输出**：`AndP()` 返回 `(True, nil)`，`OrP()` 返回 `(False, nil)`，
  叶子计数均为 0。
- **应当输出**：`predicate.go` 的 `Predicate` 文档注释明确写着
  "KindAnd, KindOr: Children (at least one)"——实现应拒绝 0 子节点
  （返回错误），或文档改为声明单位元语义。当前实现既不校验也不文档化。
- **根因分析**：`evalAnd`/`evalOr` 以单位元（True/False）为累积初值，
  对空 `Children` 循环体不执行，直接返回初值；没有任何 arity 检查。
  测试：`TestEmptyAndOr`。

## 4. Not 子节点数错误返回的是非类型化错误

- **复现输入**：`&Predicate{Kind: KindNot}`（0 子节点）或
  `&Predicate{Kind: KindNot, Children: []*Predicate{leaf, leaf}}`（2 子节点）。
- **实际输出**：`(False, error)`，错误来自 `fmt.Errorf("ontology: Not
  node requires exactly 1 child, got %d", ...)`——`IsTypeError` 与
  `IsDepthError` 均为 false，调用方无法用类型化谓词识别。
- **应当输出**：`Evaluator.Eval` 的文档注释承诺返回 "a decidable error
  (*TypeError or *DepthError)"；结构非法（arity 错误）同样应可归入某种
  类型化错误，或文档应承认存在第三类非类型化错误。同类问题还有
  `default` 分支的 "unknown predicate kind" 错误。
- **根因分析**：`evaluator.go` 的 `KindNot` 分支用 `fmt.Errorf` 构造即席
  错误，未复用 `TypeError`/`DepthError` 体系，也没有对应的 `IsXxx`
  判别函数。测试：`TestNotArityErrorIsUntyped`。

## 5. -0.0 与 +0.0 判等（与文档无冲突，仅钉住行为）

- **复现输入**：`props = {"a": math.Copysign(0, -1)}`，`Eq("a", 0.0)`。
- **实际输出**：`Eq` 为 `True`，`Lt`/`Gt` 均为 `False`（含与 `int64(0)`
  字面量比较）。
- **应当输出**：与 IEEE 754 一致，当前行为即期望行为；代码注释未对符号
  零做任何承诺，无偏差。列于此处仅为钉住行为、防止未来改动时静默回归。
- **根因分析**：`cmpOrdered` 基于 `<`/`>` 比较，IEEE 754 下
  `-0.0 == +0.0`，故三路比较得 0。测试：`TestNegativeZeroEqualsPositiveZero`。
