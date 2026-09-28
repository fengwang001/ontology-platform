# filterview — 行级过滤视图增量维护

`filterview` 包随源行的插入、删除、更新实时维护一个过滤视图，并输出视图的**净变化日志**（ViewInsert / ViewDelete）。下游按返回顺序依次应用这些净变化，始终能重建出与组件内部完全一致的视图。

## 过滤条件

- 视图 = 源表中取值落在左闭右开区间 `[Low, High)` 的全部行，判定为 `Low <= Value < High`。
- 构造器 `New(low, high)` 要求 `low < high`，否则返回 `ErrInvalidInterval`。
- 区间端点行为：`Value == Low` 属于视图，`Value == High` 不属于视图。

## 操作与输出规则

输入以批 `[]Op` 为单位，逐行校验、统一提交。

- **插入**（`OpInsert`，只填 `After`）：值在区间内输出 `ViewInsert(After)`，否则无输出。
- **删除**（`OpDelete`，只填 `Before`）：前像必须与源表当前行逐字段相等；旧值在区间内输出 `ViewDelete(Before)`，否则无输出。
- **更新**（`OpUpdate`，填 `Before`/`After`）：前像必须等于源表当前行，且前后主键相同。按前/后是否满足条件分四种情形：

| 情形 | 条件 | 输出 |
| --- | --- | --- |
| in → in，行不变 | 两侧都满足且前后行逐字段相等 | 无输出 |
| in → in，行变化 | 两侧都满足且行有变化 | `ViewDelete(Before)` 然后 `ViewInsert(After)` |
| in → out | 前满足、后不满足 | `ViewDelete(Before)` |
| out → in | 前不满足、后满足 | `ViewInsert(After)` |

另有 out → out（两侧都不满足），无输出。更新产生两条变化时顺序固定为**先撤回旧值、再写入新值**，下游按序应用即可。

## 拒绝原因

以下情况整批拒绝，返回 `*RejectError`（含批内下标 `Index` 与操作 `Op`），可用 `errors.Is` 区分原因：

- `ErrInvalidInterval`：`low >= high`。
- `ErrEmptyKey`：插入/删除/更新涉及的行主键为空（更新要求前后都非空）。
- `ErrUpdateKeyMismatch`：更新前后主键不同。
- `ErrDuplicateKey`：插入主键已存在，或同一批内主键冲突。
- `ErrKeyNotFound`：删除/更新的主键在源表中不存在。
- `ErrBeforeMismatch`：删除/更新的前像与源表当前行不逐字段相等。
- `ErrUnknownOp`：操作类型非法。

**原子性**：批内所有操作先在暂存区模拟校验，任一操作非法则整批放弃——源表、视图以及已产生的日志都不会改变（被拒绝的批不输出任何日志）。

## 一致性与确定性

- `Apply` 串行化提交；`Snapshot` 在同一读锁下返回源表与视图的深拷贝，二者逐字段对应于同一时刻，可被并发读取。
- 变化严格按批内操作顺序产生，不依赖 map 遍历；同一输入序列重复执行得到完全相同的输出。
- 通过 `SetLogger(Logger)` 安装日志器后，每个成功提交的批会打印：每条输入、每条输出净变化，以及逐条判定依据（如 `in->out: ViewDelete(old)`）。传 `nil` 关闭。

## 本地验证

```bash
# 全量测试
go test ./...

# 竞态检测 + 详细日志（含输入、输出、判定依据）
go test -race -v ./filterview

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# 格式与静态检查
gofmt -l .
go vet ./...
```

若默认 GOCACHE 所在文件系统只读，可指定可写缓存目录，例如：

```bash
GOCACHE=/tmp/go-cache go test -race -v ./filterview
```

## 最小用法

```go
m, err := filterview.New(10, 20) // 视图保留 10 <= Value < 20 的行
if err != nil { /* ErrInvalidInterval */ }

changes, err := m.Apply([]filterview.Op{
    {Kind: filterview.OpInsert, After: filterview.Row{Key: "a", Value: 11}},
    {Kind: filterview.OpUpdate,
        Before: filterview.Row{Key: "a", Value: 11},
        After:  filterview.Row{Key: "a", Value: 25}}, // in -> out
})
// changes: ViewInsert{a 11}, ViewDelete{a 11}

source, view := m.Snapshot() // 并发安全的一致快照
```
