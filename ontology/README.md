# 文件监视事件合并去抖器

`ontology/debouncer.go` 提供 `Debouncer`：把一路原始文件事件按路径折叠为净事件，
在静默期满（或到达最长等待）后按路径字节序批量吐出。

## 类型

- 原始事件 `Event{Kind, Path}`，`Kind` 为 `Create`、`Modify`、`Delete`、`DeleteDir`。
- 待发布条目每路径至多一个，含净类型（`NetCreate=C`、`NetModify=M`、`NetDelete=D`）、
  首次时刻 `first` 与末次时刻 `last`（整数毫秒）。
- 吐出项 `Entry{Path, Kind, First, Last}`。

## 折叠表（Add）

无现存条目时：`Create -> C`，`Modify -> M`，`Delete/DeleteDir -> D`，`first = last = now`。

有现存条目时（除取消外 `last = now`，`first` 不变）：

| 现存 | Create | Modify | Delete / DeleteDir |
| ---- | ------ | ------ | ------------------ |
| C    | C      | C      | 取消（条目消失，不发出） |
| M    | M      | M      | D                  |
| D    | M      | D      | D                  |

取消后条目消失，之后同路径事件按“无现存条目”重新起算（新的 `first`）。

## DeleteDir 前缀规则

`DeleteDir(p)` 先移除所有以 `p + "/"` 为前缀的待发布条目（不论净类型），
再对 `p` 自身按 Delete 折叠。因此 `DeleteDir("a")` 吞并 `a/b`、`a/b/c`，
但不吞并 `ab/c`（`"ab/"` 不是其前缀）。

## 到期判定（Flush / NextDue）

- 条目到期当且仅当 `now - last >= Q` 或 `now - first >= W`。
  `Q` 是静默期（差 1 不到期，恰好 `Q`/`W` 到期）；`W` 是最长等待，
  即使持续有事件使静默期永不满足，`first + W` 也会强制吐出。
- `Flush(now)` 吐出全部到期条目并移除，按路径字节序升序返回。
- `NextDue()` 返回 `min(last+Q, first+W)` 的全局最小值；无条目时返回“无”。

## 非法输入与错误优先级

构造（按此顺序只报第一个）：`Q < 1` -> `ErrInvalidQuietPeriod`；
`W < Q` -> `ErrInvalidWaitLimit`；`Cap < 1` -> `ErrInvalidCapacity`。

`Add`（按此顺序只报第一个）：

1. 种类未知 -> `ErrUnknownKind`
2. 路径非法（为空、以 `/` 开头或结尾、含连续 `//` 空段）-> `ErrInvalidPath`
3. 时钟回拨（`now` 小于此前任一次**已接受**的 Add/Flush 的最大 now）-> `ErrClockRewind`
4. 待发布数已达 `Cap` 且该路径当前无条目 -> `ErrCapacityExceeded`
   （该判定在 DeleteDir 清除子孙**之前**进行；路径自身有条目时不受容量限制）

`Flush` 只有时钟回拨一种错误（`ErrClockRewind`）。
任何被拒绝的操作都不会改变条目、最大 now 与已吐出结果。

## 并发与确定性

`Add`、`Flush`、`NextDue` 由互斥锁保护，结果等价于某个串行顺序。
相同的 `(now, 事件)` 序列重放得到完全相同的吐出序列。

## 本地验证

```bash
# 全量测试（含 2000 组随机对拍，-v 打印输入/输出/判定依据）
go test -race -v ./ontology/

# 仅看对拍日志（前 3 个 seed 打印完整输入与输出）
go test -run TestDifferentialAgainstNaive -v ./ontology/

go vet ./...
gofmt -l .
```

若默认构建缓存目录只读，可设置 `GOCACHE=/tmp/go-cache`。
