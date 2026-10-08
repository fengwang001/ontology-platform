# 对外接口与使用说明

对外能力集中在 `internal/api`，内部错误被归一化为稳定错误码，不泄漏存储层类型。

## 写入 / 删除

```go
svc := api.New()

// 新建：凭证传 "" 或 "v0"
r1, err := svc.Put("Person", "p1", 10 /*业务起点*/, "", "alice")

// 继续写入必须带上“当前最新版本”凭证
token := svc.LatestToken("Person", "p1") // "v1"
r2, err := svc.Put("Person", "p1", 30, token, "alice@30")

// 逻辑删除：从业务时间 50 起事实不再成立（仍占用一个系统版本号）
if _, err := svc.Delete("Person", "p1", 50, svc.LatestToken("Person", "p1")); err != nil { ... }

// 复活：删除之后在更大业务起点再次写入
svc.Put("Person", "p1", 80, svc.LatestToken("Person", "p1"), "alice-reborn")
```

### 乐观并发凭证

- 形如 `"v<N>"`；`""` 与 `"v0"` 等价，表示“要求该主键尚不存在”。
- 凭证格式错误（缺 `v` 前缀、非数字、负数）归 `INVALID_ARGUMENT`。
- 格式正确但不等于最新版本归 `OPTIMISTIC_CONFLICT`；调用方应重新读取最新凭证后重试。

## 双时态查询

```go
res, err := svc.Query("Person", "p1",
    3,          // asOfSys：系统时间（“我要回到哪次写入之后看”）
    40)         // bizAt  ：业务时间（“事实在何时成立”）
// res.Status: ALIVE | DELETED | NEVER_WRITTEN
```

返回字段：`Status`、`SysVersion`（定位到的版本）、`BizStart`、`Payload`。

历史时间轴重建（系统时间 S 时刻可见的全部业务区间）：

```go
segs, ok, err := svc.History("Person", "p1", 3)
// ok=false 表示该键在 S 时尚不存在；segs 按业务起点升序，末段 End 为 math.MaxInt64
```

## 错误码

| 错误码 | 触发条件（按此优先级，只报第一个） |
| --- | --- |
| `INVALID_ARGUMENT` | 对象类型/主键为空、业务时间 <0、凭证格式非法、存活写入负载为空 |
| `OPTIMISTIC_CONFLICT` | 凭证与当前最新版本不一致 |
| `BEFORE_EARLIEST_BIZ` | 业务起点早于该键已提交的最早可追溯边界 |

```go
var ae *api.Error
if errors.As(err, &ae) {
    switch ae.Code {
    case api.CodeConflict:
        // 重新获取 LatestToken 后重试
    }
}
```

## 并发

`Service` 可被多个 goroutine 并发使用。写串行化在存储层完成；查询用读锁，
与写入互斥但彼此并行。
