# firstrow — 变更流保留首条去重

`firstrow` 包在插入/撤回（insert/retract）变更流上，为每个键维护存活行集合，
并跟踪每个键排序键最小的“首条”行。下游只要按输出顺序应用日志，
就能始终得到与全量存活集合一致的每个键的首条结果。

入口：`firstrow.New(firstrow.Options{...})` → `Dedup.Apply(changes)` / `Dedup.Snapshot()`。

## 排序规则

每行的排序键按以下顺序比较，全部为升序：

1. `Row.Time`：可负的 `int64` 时间字段，数值越小越靠前。
2. `Row.ID`：时间相同时按标识的字典序（Go `<` 字符串比较，UTF-8 字节序）打破平局。

因此不存在排序键完全相同的两行（同一键下 `ID` 唯一），首条唯一且确定，
与 map 迭代顺序、goroutine 调度等无关。

## 首条判定与输出规则

`Apply` 按批内顺序逐条处理变更，比较每条变更处理前后该键的首条：

- 插入后出现更小的首条（含该键从无到有）：先输出旧首条的 `Retract`，
  再输出新首条的 `Insert`；该键此前无存活行时只输出 `Insert`。
- 撤回当前首条：先输出该首条的 `Retract`；若还有存活行，
  再输出提升上来的次早行（新的最小排序键）的 `Insert`；
  若无存活行则只输出 `Retract`，该键从快照中消失。
- 首条不变（插入更大的行、撤回非首条行）：不输出任何条目。

输出严格保证“先撤回旧首条、再写入新首条”的相邻顺序，
下游顺序重放该日志即可始终得到正确首条，不会观察到键缺失或双写。

## 非法输入与原子性

下列变更会被拒绝，错误为 `*firstrow.RejectError`，含批内下标 `Index`
与可区分的 `Reason`：

| 原因常量 | 触发条件 |
| --- | --- |
| `ReasonEmptyKey` | `Row.Key` 为空（字段校验先于空标识判定） |
| `ReasonEmptyID` | `Row.ID` 为空 |
| `ReasonDuplicateID` | 插入的 `(Key, ID)` 已经存活 |
| `ReasonUnknownID` | 撤回的 `(Key, ID)` 当前不存活 |
| `ReasonLimitExceeded` | 该插入会使全部键合计存活行数超过 `Options.MaxLiveRows` |

拒绝以批为单位：验证在惰性复制的工作副本上进行，
一旦遇到非法变更，整批丢弃，真实存活行、存活计数与已提交日志均不改变。

## 并发与确定性

- `Dedup` 内部用读写锁保护：`Apply` 串行化写入，`Snapshot`/`LiveRows`
  可与写入并发执行；`Snapshot` 在同一把读锁内复制所有键的当前首条，
  返回的是逐键一致的单一时刻快照。
- 相同的变更序列在新实例上反复计算，输出条目序列与快照逐字节相同
  （已由确定性重放测试覆盖）。

## 日志

传入 `Options.Logger`（`*slog.Logger`）后，每条变更都会打印：

- `input change`：输入条目的操作、键、标识、时间及批内下标；
- `output entry`：输出的撤回/写入条目及判定依据（`because` 字段）；
- `decision`：接受但首条不变、无输出的判定依据；
- `change rejected ...`：拒绝原因及“整批丢弃、状态不变”的说明。

`firstrow.NewTextLogger(w)` 提供去除时间戳的文本 logger，
便于把相同输入序列的日志做字节级比对。

## 本地验证

```bash
# 全量测试（含竞态检测）
go test -race ./...

# 仅本包 + 详细输出
go test -race -v ./firstrow

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# 格式与静态检查
gofmt -l .
go vet ./...
```

若默认的 Go 构建缓存目录只读，可指定可写缓存：

```bash
GOCACHE=/tmp/go-cache go test -race ./...
```
