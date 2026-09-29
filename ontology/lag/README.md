# lag：窗口前驱值（LAG）增量维护

`ontology/lag` 对"按分区排序的行变更流"增量维护每行的前驱取值
（窗口函数 `LAG(value, 1)`），并输出确定顺序的变更日志。下游按序应用
日志后的视图与引擎视图、以及对全量数据批量重算的结果逐行一致。

## 排序与前驱取值

- 行结构：`Row{Partition, ID, SortKey, Value}`。
- 分区内排序键：`SortKey` 升序；`SortKey` 并列时按 `ID` 字典序升序打破。
- 某行前驱 = 同分区内紧邻其前那行的 `Value`；分区首行前驱严格为空。
- 空（null）与零值字符串 `""` 严格区分：视图与日志用 `HasPrev bool` +
  `Prev string` 表示，`HasPrev=false` 即前驱为空。
- 不同分区相互独立、互不影响；分区在最后一行删除后消失。

## 变更日志规则

一次 `Insert` / `Delete` 返回一个 `[]Change`，顺序固定：

- 插入：先输出新行 `insert`（携带其前驱值）；若紧邻后继的前驱取值因此
  改变，再输出该后继的 `update`。
- 删除：先输出被删行 `delete`；若紧邻后继的前驱取值因此改变（含
  非空 ↔ 空），再输出该后继的 `update`。
- 前驱值未变化的后继**不输出任何条目**（例如前后相邻两行 `Value` 相同，
  删除中间行后继取值不变）。
- 因此只有至多两条输出，且同一输入永远产生同一顺序、同一内容的日志，
  与插入历史无关、可复现。

`Change.Kind` 取值：`insert` / `delete` / `update`。

## 边界与错误类别

非法输入被**整体拒绝**：返回 `*RejectError`（含互不相同的 `Reason`），
不写入行、不改视图、不产生日志条目（失败不留痕）。拒绝原因：

| Reason | 触发条件 |
| --- | --- |
| `duplicate_id` | 向同分区插入已存在的 `ID` |
| `missing_id` | 删除的 `ID` 在该分区不存在 |
| `empty_partition` | 分区名为空字符串（插入/删除） |
| `empty_id` | 行标识为空字符串（插入/删除） |
| `row_limit_exceeded` | 插入后总行数超过引擎上限（`WithMaxRows`，默认 1,000,000） |

## 并发与一致性

- `sync.RWMutex` 保护：`Insert` / `Delete` 串行提交；
  `Rows` / `Snapshot` / `AllSnapshot` / `Partitions` / `Verify` 为读路径，
  可被多个执行体并发调用，且可与提交并发。
- 同一组行以任意顺序插入，最终视图逐标识相同；测试中以独立的
  "日志回放视图"和独立批量重算双重校验。
- `Verify()` 用独立重算逐分区、逐行比较前驱值，任何不一致即返回错误。

## 逐步日志

`New(WithLogger(func(format string, args ...any){ ... }))` 可安装日志钩子，
每步打印 `INPUT`（输入）、`DECIDE`（判定依据：前驱是谁、为何输出/不输出
后继修正、拒绝原因）与 `OUTPUT`（输出条目数）。测试中接到 `t.Logf`，
用 `go test -v` 可见每步输入、输出与判定依据。

## 本地验证

```bash
# 全部用例（含竞态检测；-v 查看逐步输入/输出/判定日志）
go test -race -v ./ontology/lag

# 全仓库测试、静态检查、格式检查
go test ./...
go vet ./...
gofmt -l .
```
