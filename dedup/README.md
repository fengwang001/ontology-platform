# dedup：多源并集增量去重维护器

`dedup.Maintainer` 按分区（数据源）维护元素集合，并通过全局引用计数增量维护
所有分区的**去重并集视图**。增量维护的结果与“对所有分区做一次批量重算”
（`SelfCheck` 即据此实现）始终一致。

## 引用计数规则

- 每个分区是一个集合，同一元素在同一分区内至多出现一次。
- 元素的全局引用计数 = 当前持有该元素的分区个数。
- 视图 = 所有引用计数不小于 1 的元素集合，因此一个元素无论被多少分区持有，
  在视图中只出现一次。
- 计数归零时删除该元素的计数键，元素从视图撤下。

## 幂等语义

- **加入** `Add(p, e)`：若 `e` 已在分区 `p` 中，无操作；否则写入分区并把
  引用计数加一。计数由 `0 -> 1` 时向变更日志追加一条 `KindAdd`，其余情况无输出。
- **撤回** `Remove(p, e)`：若 `e` 不在分区 `p` 中，无操作；否则从分区删除并把
  引用计数减一。计数由 `1 -> 0` 时追加一条 `KindRemove`，其余情况无输出。
- 重复加入、撤回不存在的元素都可以安全地反复调用，不会重复计数或产生重复日志。
- 变更日志（`Log`）严格按操作顺序追加，加减条目配对，下游按序应用即可复现视图。

## 去重并集示例

| 操作 | 分区持有情况 | 引用计数 | 视图 | 日志 |
| --- | --- | --- | --- | ---|
| `Add(0, a)` | `{0:{a}}` | `a=1` | `{a}` | `+a` |
| `Add(1, a)` | `{0:{a}, 1:{a}}` | `a=2` | `{a}` | 不变 |
| `Add(0, a)` 重复 | 同上 | `a=2` | `{a}` | 不变 |
| `Remove(0, a)` | `{1:{a}}` | `a=1` | `{a}` | 不变 |
| `Remove(1, a)` | `{}` | `a=0` | `{}` | `-a` |
| `Remove(1, a)` 不存在 | `{}` | `a=0` | `{}` | 不变 |

## 错误处理（三类互不相同、可判定的错误）

用 `errors.Is` 判定：

- `ErrInvalidPartitionCount`：`New(n)` 时 `n <= 0`。
- `ErrPartitionOutOfRange`：分区下标为负或不小于分区数。
- `ErrEmptyElement`：元素为空字符串。

批量接口 `AddBatch` / `RemoveBatch` 先对整批做预检：**任一条被拒则整批不生效**，
	分区集合、引用计数、日志、视图均保持调用前状态。同一 `(分区, 元素)` 在一批内
	重复出现只生效一次。

## 并发

- 所有方法并发安全，内部使用 `sync.RWMutex`：写操作互斥串行化，读操作与写操作互斥。
- `View`、`Log` 返回的是快照副本，调用方修改不影响内部状态。
- 不存在并发写时，多个 goroutine 并发只读同一实例，得到的视图必然逐字段相同；
  并发期间的每个快照也都是某个已提交的一致状态。

## 主要 API

- `New(n int) (*Maintainer, error)`
- `Add(partition int, element string) error`
- `Remove(partition int, element string) error`
- `AddBatch(ops []Op) error` / `RemoveBatch(ops []Op) error`
- `View() map[string]struct{}` / `Has(element string) bool` / `RefCount(element string) int`
- `Log() []Change`（`Change{Kind, Element, RefCount}`，`KindAdd` / `KindRemove`）
- `SelfCheck() error`：按分区集合重算计数并校验视图与日志不变量

## 本地验证

```bash
# 详细输出（日志含每条用例的输入、结果与判定依据）
go test -v ./dedup

# 竞态检测
go test -race ./dedup

# 覆盖率
go test -coverprofile=/tmp/coverage.out ./dedup
go tool cover -html=/tmp/coverage.out

# 静态检查与格式
go vet ./...
gofmt -l .
```

测试覆盖：跨分区持有不撤回、已在视图不重复加、不在分区不撤回、
三类非法输入被拒后状态不变、批量原子性、增量结果与批量重算逐字段等价、
并发只读视图一致（含 `-race` 读写并发）。
