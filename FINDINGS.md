# FINDINGS — 实测结论

## 表1：A→B→C + 并行慢任务（两模式四类状态分布）
（图：A 失败；A→B→C；S 为独立慢任务；快失败取消 S，尽力而为 S 成功。）

| 模式 | A | B（原因） | C（原因） | S |
|---|---|---|---|---|
| FailFast | Failed | Skipped→A | Skipped→A | Cancelled |
| BestEffort | Failed | Skipped→A | Skipped→A | Success |

## 表2：四类故障注入结论

| 注入 | 结论 |
|---|---|
| 环（含自环） | _待 exec 测试后回填_ |
| panic | _待回填_ |
| 多任务同时失败 | _待回填_ |
| 取消后写回 | _待回填_ |

## 分批测试记录
- graph（`go test ./graph`）：空图/单点/无依赖/1000 层链（无栈溢出，1000 层）/菱形分层=3/自环/双环/带尾环均检出，环路径首尾闭合且逐边真实；重复加边入度仍为 1；缺失节点返回可 `errors.Is(ErrMissingNode)` 的错误。
