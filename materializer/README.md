# materializer：全或无原子批次物化器

把一个批次（`[]Event`）内对键值视图的写入/删除，按**批内顺序**预演；
只有全部事件的前置期望都满足，才把结果**一次性**替换到可见视图。
下游任意时刻读到的要么是整批前、要么是整批后，不存在半批次中间态。

## 数据模型

```go
type Event struct {
    Key    string
    Op     Op      // Put 或 Delete
    Value  string  // Put 时写入的值
    Expect *string // 前置期望：nil 要求键不存在；非 nil 要求当前值恰好相等
}
```

## 批内预演规则

1. 预演在「真实视图的副本 + 本批前序事件效果」构成的暂存视图上进行，
   暂存视图在提交前对读取者不可见。
2. 按下标从小到大逐条检查前置期望：
   - `Expect == nil`：要求该键在暂存视图中**不存在**；
   - `Expect != nil`：要求该键存在且值与 `*Expect` **完全相等**。
3. 期望的判定对前序效果可见：同键先前的 `Put` 会覆盖真实值，先前的
   `Delete` 会使键消失（因此后续事件可以用「期望不存在」重写）。
4. 期望通过后才应用本条事件（`Put` 覆盖、`Delete` 删除）。
5. 遇到**第一条**不满足期望（或非法输入）的事件立即停止，整批拒绝，
   返回 `*FailureError`（`Index` 为失败下标，`Err` 为哨兵错误，错误
   文本同时给出期望值与实际值作为判定依据）。后续事件不再检查。

## 原子生效语义

- 全批预演通过后，用单次原子指针切换把暂存视图变为可见视图
  （`atomic.Pointer[map]`）；读取路径（`Get` / `Snapshot`）不持锁，
  因此可与批应用以及彼此并发。
- 批次之间由互斥锁串行化，保证判定与生效的可复现性。
- 任何拒绝都不触碰可见视图（失败无痕），被拒后物化器可继续正常使用。
- `Snapshot()` 返回深拷贝，调用方修改返回值不影响内部状态。

## 三类互不相同的可判定错误

| 哨兵错误 | 触发条件 |
| --- | --- |
| `ErrEmptyBatch` | 批次为空（长度 0） |
| `ErrEmptyKey` | 第一条空键事件（即使该事件尚未执行任何变更） |
| `ErrExpectationFailed` | 第一条前置期望不满足的事件 |

用 `errors.Is(err, materializer.ErrEmptyKey)` 等判定类别，
用 `errors.As(err, &fe)` 取出 `*FailureError` 的下标与事件。

## 日志

`New(logger)` 接受一个 `*log.Logger`（传 `nil` 关闭日志）。每次 `Apply`
都会打印：完整输入批次、每条事件的判定依据（期望 vs 暂存实际值）、
最终结果（`apply commit` 或 `apply reject ... state unchanged`）。

## 本地验证

```bash
# 单元测试 + 竞态检测（含并发只读完整批次边界的压测）
go test -race -v ./materializer/

# 全量测试 / 覆盖率
go test ./...
go test -coverprofile=coverage.out ./materializer/
go tool cover -html=coverage.out

# 代码检查
gofmt -l .
go vet ./...

# 演示：链式期望、全或无拒绝、空批与空键
go run ./cmd/materializer-demo
```

## 测试覆盖场景

- 同键先写后删（删除事件能看到前序写入的新值）；
- 链式期望（后写覆盖前写、前序删除对「期望不存在」可见）；
- 全或无：批次中段失败时前段暂存变更不可见，状态逐字节不变；
- 错误定位第一条失败事件（下标正确，不报告后续事件）；
- 空批 / 空键 / 前置期望失败三类错误互不相同且拒绝后状态不变；
- 并发读只见完整批次边界（80 批 × 12 键同值不变量 + 6 个并发读取协程）；
- 并发 `Apply` 串行化；`Snapshot` 深拷贝隔离；日志含输入、结果与判定依据。
