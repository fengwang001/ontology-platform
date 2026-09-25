# FINDINGS：实现行为与注释/文档承诺的不一致

下列每条均由 `edgecases_test.go` 中的 characterization test 钉住，
断言的是**当前真实行为**（测试全部通过），而非期望行为。

## 1. `LeafCount` 不数 `KindIsNull` 叶子

- **复现输入**：`AndP(IsNull("missing"), Eq("a", 1))`，`props={"a":1}`。
- **实际输出**：结果 `True`，`Result.LeafCount() == 1`；单独求值
  `IsNull("missing")` 时 `LeafCount() == 0`。
- **应当输出**：`KindIsNull` 同样是叶子谓词；若按
  `Result`/`LeafCount` 注释「how many leaf comparisons were actually
  evaluated」的字面含义，它被实际求值一次，应计为 1；或在注释中
  明确「仅统计 `KindCompare`」。
- **根因**：`evaluator.go` 的 `eval` 中只有 `case KindCompare` 分支
  执行 `r.leaves++`；`case KindIsNull` 分支直接返回，无计数。计数
  口径（「叶子数」还是「比较数」）未在注释中界定。

## 2. int64 经 `float64()` 转换，超过 2^53 精度丢失

- **复现输入**：`base = 1<<53`，`neighbor = base+1`；
  `Eq("a", float64(base))`，`props={"a": neighbor}`。
- **实际输出**：`True`（`Eq`），且
  `Lt("a", float64(base))` / `Gt("a", float64(base))` 均为 `False`；
  两个不同的 int64（`base` 与 `neighbor`）做 Eq 也得 `True`。
- **应当输出**：`neighbor != base`，Eq 应为 `False`；int64 与
  float64「互可比」应保持 int64 一侧的整数精度（如分别按精确值
  比较，或 int64 之间走整数比较）。
- **根因**：`compare.go` 的 `compareLeaf` 对 `int64` 属性值调用
  `compareNumeric(..., float64(a), ...)`，`compareNumeric` 对 int64
  字面量同样 `b = float64(l)`。双侧都被舍入到最接近的 float64；
  `2^53+1` 不可表示，回舍为 `2^53`，于是判等。注释仅声称
  「int64 and float64 are mutually comparable」，未声明精度上限。

## 3. 空 `AndP()` / `OrP()` 被静默接受并返回恒等元

- **复现输入**：`AndP()`（零子节点）、`OrP()`（零子节点）。
- **实际输出**：空 And 返回 `True`，空 Or 返回 `False`；
  `Not(AndP()) == False`、`Not(OrP()) == True`，均无错误。
- **应当输出**：`predicate.go` 中 `Predicate` 文档声明 And/Or 的
  Children「at least one」。要么构造/求值时拒绝零子节点（报错），
  要么文档明确空集合按恒等元（And=True、Or=False）处理。
- **根因**：`AndP`/`OrP` 是简单的 variadic 包装，不做校验；
  `evalAnd`/`evalOr` 以 `result := True`/`False` 为初值遍历
  `Children`，空切片时循环体不执行，直接返回初值。

## 4. `IsNull` 判定的是「键是否存在」，值为 `nil` 不算缺失

- **复现输入**：`props = {"x": nil}`；先求 `IsNull("x")`，再求
  `Eq("x", 1)`。
- **实际输出**：`IsNull("x") == False`（键存在）；同一属性走
  Compare 时返回 `*TypeError`（reason `unsupported attribute type`），
  不是 `Unknown`。
- **应当输出**：需要在文档中钉死语义。若「IsNull = 键缺失」，则
  当前行为自洽，但应说明 `nil` 值与缺失的区别，并说明对 `nil`
  值做 Compare 报 `TypeError`；若 IsNull 想表达「值为空」，则
  `{"x": nil}` 应返回 `True`。
- **根因**：`eval` 的 `KindIsNull` 分支用
  `_, ok := props[p.Attr]` 只检查键存在性；而 `compareLeaf` 的
  类型 switch 没有 `nil` 分支，`nil` 落入 `default` 产生
  `*TypeError`。

## 5. `KindNot` 子节点数不为 1 时返回不可分类的普通错误

- **复现输入**：`&Predicate{Kind: KindNot}`（0 个子节点）或
  `&Predicate{Kind: KindNot, Children: [eq, eq]}`（2 个）。
- **实际输出**：返回 `fmt.Errorf("ontology: Not node requires
  exactly 1 child, got N")`；`IsTypeError(err) == false`、
  `IsDepthError(err) == false`，返回值 `False`。
- **应当输出**：`Eval` 注释承诺返回「a decidable error
  (`*TypeError` or `*DepthError`)」。结构非法的谓词树属于调用方
  可判定并修正的错误，应归入 `*TypeError`（或新增结构错误类型），
  使错误分类函数能识别；否则需在注释中声明第三类普通错误。
- **根因**：`eval` 的 `KindNot` 分支用裸 `fmt.Errorf`，未构造
  `TypeError`，因此 `errors.As` 无法匹配。同分支的 arity 检查先于
  递归，子节点完全不被求值（`LeafCount` 保持 0），这一次序也属
  未文档化行为。

## 6. 深度检查先于叶子计数，恰在 maxDepth 的叶子计入

- **复现输入**：`NewEvaluator(1)` 求 `Eq("a",1)`（叶子 depth=1）；
  再求 `NotP(Eq("a",1))`（叶子 depth=2）。
- **实际输出**：前者成功且 `LeafCount()==1`；后者立即返回
  `*DepthError`，`LeafCount()==0`（被拒叶子未计数）。
- **应当输出**：当前行为本身自洽（根 depth=1，`depth > maxDepth`
  在进入节点时判定），但 `NewEvaluator` 注释只说「deeper trees
  yield a `*DepthError`」，未说明根深度从 1 起算、边界是严格大于、
  以及被拒节点不计入叶数。注释应把这三点写清。
- **根因**：`eval` 在函数入口先做 `if depth > e.maxDepth` 检查，
  之后 `KindCompare` 分支才 `r.leaves++`；因此深度超限的节点在
  计数递增之前即返回错误。

## 测试位置

- `edgecases_test.go`：六个表驱动 characterization test，
  分别对应上述六条；全部断言当前真实行为，`go test -race ./...`
  通过。未改动任何既有实现文件。
