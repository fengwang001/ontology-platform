# 测试结论

命令：`go test -count=1 ./...` 与 `go test -race -count=1 ./...` 全部通过。

## 表 1：两个探针在三种方案下的结果

| 探针 | 甲（不可见=假） | 乙（不可见=NULL） | 丙（本实现） |
|---|---|---|---|
| `NOT (secret = 1)` | 放行，返回全部行（NOT false 恒真，泄露存在性+可盲注） | UNKNOWN，看似过滤但 `x OR secret=1` 仍可区分 UNKNOWN/FALSE | 拒绝：`ErrInvisibleColumn`，列 secret，路径 not>compare，不返回行 |
| `secret IS NULL` | 比较按假，无行（但 NOT 探针仍泄露） | 放行，返回全部行（IS NULL 成存在性探针） | 拒绝：`ErrInvisibleColumn`，列 secret，路径 compare，不返回行 |

反例断言：`filter` 包 `TestProbesRejected` 对两个探针均断言
`errors.Is(err, ErrInvisibleColumn)` 且 `Rows == nil`——即本实现不会给出
甲/乙会给出的那个「返回行」答案。

OR 常量折叠例外（`TestORConstantFoldException`）：
`OR(true, secret=1)` 放行，secret 记 elided（折叠后该支永不读取）；
`OR(a=1, secret=1)` 不折叠仍拒绝；`AND(false, secret=1)` 按定义不享例外仍拒绝。

## 表 2：32 种穷举组合结论（8 种列可见性 × 4 种谓词形态）

模板：列 a/b/secret；`=` 与 `NOT` 只引用 secret；
`AND`/`OR` 引用 a 与 secret；定义为「引用列全部可见才放行」。

| 谓词形态 | 放行数 | 拒绝数 | 放行的可见性条件 |
|---|---|---|---|
| `=`（secret=1） | 4 | 4 | secret 可见（a/b 任意） |
| `NOT`（NOT secret=1） | 4 | 4 | secret 可见（a/b 任意） |
| `AND`（a=1 AND secret=1） | 2 | 6 | a、secret 同时可见（b 任意） |
| `OR`（a=1 OR secret=1） | 2 | 6 | a、secret 同时可见（b 任意） |
| 合计 | 12 | 20 | — |

数值由 `TestExhaustive32` 单循环覆盖（无展开用例），逐组合断言与上表一致；
`-v` 日志打印每形态 allow/reject 计数。

## 其他关键断言

- 行裁剪：不可见列物理移除，仅 2 个可见键存在；与 `{"secret":""}`
  置零值可区分（`TestRowProjection`）。
- 确定性：报告在打乱输入下逐字节相同；同列多次引用归并一条、路径全保留
  （`report.TestBuildDeterministic`、demo 20 次打乱）。
- 复杂度：节点访问数 == `predicate.Count`（一遍）；1000 列 5 可见时
  拷贝恰 5 次、<= `4*5`（`TestCounters`）。
- 三类错误 `ErrInvisibleColumn` / `ErrEmptyRole` / `ErrUnknownRole`
  均以 `errors.Is` 区分；常量谓词不引列，空可见集角色可放行（行投影为空）。
