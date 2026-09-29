# 按键节流物化视图刷新器

`throttle.Refresher` 使用调用方提供的逻辑时间，对每个键独立维护一个待刷新批次。视图只有在批次实际刷新时才更新，因此待刷新值不会提前对外可见。

## 节流合并

- 新键的首次变更创建待刷新批次，计数为 `1`，计划刷新时刻为 `到达时刻 + interval`。
- 同一键在刷新前再次变更时，并入现有批次：计数加一、值覆盖为最新值、计划刷新时刻顺延为最新到达时刻加 `interval`。
- 不同键互不影响；待刷新键数量达到上限时，新键会返回 `ErrTooManyPendingKeys`，已有键的后续变更仍可并入。
- `Advance(now)` 批量刷新所有 `ScheduledAt <= now` 的批次，等号边界也会刷新。
- 同一次刷新按键名升序产出，保证多键同时到期时输出顺序可复现。
- `Shutdown(at)` 忽略尚未到达的计划时刻，立即刷新所有待刷新批次，并关闭实例。

每条 `RefreshRecord` 表示一个已提交批次：

- `Key`：刷新的键。
- `Value`：批次中最后一次到达的值。
- `Count`：被合并的变更条数。
- `ScheduledAt`：最后一次顺延后的计划刷新时刻。
- `RefreshAt`：批次真正应用到视图的逻辑时刻；停机早刷时会早于 `ScheduledAt`。

## 时钟与错误

- 逻辑时钟单调不减：成功的 `Change`、`Advance`、`Shutdown` 都会把最后时间推进到本次输入时间。
- 时间戳相等是合法的；同一键在相同逻辑时间连续到达时，按调用顺序由后者覆盖前者。
- 早于最后成功操作时间的调用返回 `ErrTimeBeforeLast`。
- 非法输入先完成校验再改状态，因此拒绝不会改变逻辑时钟、待刷新批次、视图或累计批数。

可判定错误如下：

- `ErrNonPositiveParameter`：`interval <= 0` 或 `maxPendingKeys <= 0`。
- `ErrEmptyKey`：变更键为空字符串。
- `ErrTimeBeforeLast`：输入时间早于最后成功操作时间。
- `ErrTooManyPendingKeys`：新键会使待刷新键数量超过上限。

另有 `ErrRefresherClosed` 表示 `Shutdown` 后继续提交变更，便于调用方区分生命周期错误。

## 并发读取

`Snapshot`、`View`、`TotalBatchCount`、`PendingKeyCount` 和 `LastLogicalTime` 都使用同一把互斥锁。`Snapshot` 会深拷贝视图与历史记录切片；在没有并发写入的前提下，多个 goroutine 并发读取同一实例会得到逐字段相同的结果，并且调用方修改返回的 map 或切片不会影响刷新器内部状态。

## 本地验证

```bash
# 全量测试；-v 会打印每个场景的输入、结果与判定依据
go test -v ./...

# 竞态检测
go test -race ./...

# 静态检查与格式化检查
go vet ./...
gofmt -l .
```

如果系统默认 Go 缓存目录不可写，可指定临时缓存：

```bash
GOCACHE=/tmp/go-cache-ontology go test -race -v ./...
```
