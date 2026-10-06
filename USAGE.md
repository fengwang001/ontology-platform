# 使用说明

当前包名为 `store`；在模块 `ontology` 内直接使用同包 API。

## 基本读写

```go
s := store.New()

secondary := "employee-email"
lsn, err := s.Put(store.Row{
    Primary:   "user-1",
    Secondary: &secondary,
})

row, err := s.Get("user-1")
lsn, err = s.Delete("user-1")
```

`Secondary == nil` 表示空二级键，不进入唯一索引。空字符串主键和空字符串查询参数返回 `ErrInvalidArgument`。

## 二级查询

```go
primary, at, err := s.Find("employee-email")
```

- 找到时返回主键和当前已追平的日志序号。
- 没有索引项时返回空主键、序号 0、错误为 `nil`。
- 索引水位落后时返回 `ErrIndexBehind`。

## 分批追赶

```go
for !s.IndexCaughtUp() {
    advancedTo, err := s.CatchUp(100)
    if errors.Is(err, store.ErrLogGap) {
        // 日志不连续；索引和水位未被该批次修改。
    }
}
```

`maxRecords` 必须非负；传 0 不推进。追赶期间仍可执行 `Put`、`Delete` 和 `Get`，但 `Find` 与 `Check` 会返回 `ErrIndexBehind`。

## 重启与自检

```go
state := s.PersistedSnapshot()

restarted := store.New()
if err := restarted.Restart(state); err != nil {
    // ErrLogGap 表示日志不连续，ErrInvalidArgument 表示持久化状态非法。
}

report, err := restarted.Check()
for _, mismatch := range report.Mismatches {
    switch mismatch.Type {
    case store.MismatchExtra:
    case store.MismatchMissing:
    case store.MismatchWrongPrimary:
    }
}
```

生产代码通常不需要直接检查索引条目；`PersistedSnapshot` 主要用于持久化适配、崩溃复现和测试。
