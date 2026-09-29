# segmentlog：分段日志与两阶段保留

`segmentlog` 提供并发安全的追加式分段日志：记录按容量滚入不同段，
保留（retention）以**整段**为最小删除单位，因此删除后剩余段依旧首尾相接，
构成连续、可复现的可读字节区间。

## 数据模型

- `Record{Time, Data, Bytes}`：一条记录。`Bytes` 必须为正且等于
  `len(Data)`，`Time` 必须非零。
- 段（segment）：一段连续记录，记录字节数累加到 `MaxSegmentBytes` 为止；
  最新一段为**活动段**，其余段密封只读。
- 全局位点：记录在逻辑字节流中的位置只增不减。段的区间为
  `[StartOff, EndOff)`，密封段的 `EndOff` 恰等于下一段的 `StartOff`。

## 追加与滚动

- `Append` 在同一把互斥锁内原子完成校验与写入，返回该记录首字节位点。
- 活动段不存在，或追加后 `段字节数 + 记录字节数 > MaxSegmentBytes` 时，
  **先新建活动段再写入**（即“已满则先滚动”）。因此：
  - 恰好填满段容量不会立即滚动，下一条记录才滚动；
  - 单条恰等于段容量的记录总会进入新段并被接受；
  - 单条超过段容量的记录以 `ErrInvalidRecord` 拒绝。
- 时间戳必须单调不减（相等允许）。早于已接受最大时间戳返回
  `ErrClockBackwards`。

## 两阶段保留

`Retain(now)` 返回 `*Report`，内含每个阶段对每段的 `Decision`（删除与否
及判定依据）。两阶段都**从最旧段起逐段判定，遇第一个不满足条件的段即停**。

1. **时间阶段**（`MaxAge > 0` 时启用）：段的保留时间取段内记录时间戳的
   最大值（`LastTime`）。当且仅当 `LastTime < now-MaxAge` 时删除该段；
   边界相等（`LastTime == now-MaxAge`）保留。遇到保留段即停止，
   后续段（含活动段）不再判定。
2. **大小阶段**（`MaxTotalBytes > 0` 时启用）：在时间阶段删除后的新段列表
   上重新逐段判定：若当前总字节数 `> MaxTotalBytes`，删除最旧段；每删一段
   重新比较，直到总量 `<= MaxTotalBytes` 或到达活动段。总量恰好等于上限即
   停止。

其他不变量：

- **活动段永不删除**：即使其时间过期或总量超限；因此上限之下最终至少保留
  活动段，且总字节数最多超出上限一个活动段。
- 删除的总是前缀整段：`StartOffset` 按删除字节数前移，单调不减；
  剩余段重新首尾相接，起始位点之后无空洞。
- `Retain` 的 `now` 早于已接受最大时间戳时返回 `ErrClockBackwards`。

## 读取

`Read(start, maxBytes)` 从记录边界位点 `start` 起，返回累计字节不超过
`maxBytes` 的连续完整记录（预算放不下下一条时停止，已放入的仍返回）。
- `start` 必须等于当前起始位点或某条记录边界；起点等于当前末尾返回空结果。
- 起点位于已删除区间、越过末尾或未对齐时返回 `ErrOutOfRange`；
  `maxBytes <= 0` 同样为 `ErrOutOfRange`。
- 读取使用读锁并返回内容副本，不改变任何状态；与追加/保留并发时，
  若两次调用间起点被前移，旧起点返回 `ErrOutOfRange`，调用方取新起点重试即可。

## 错误类别（互不相同，可用 `errors.Is` 区分）

| 错误 | 触发条件 |
| --- | --- |
| `ErrInvalidConfig` | `MaxSegmentBytes <= 0`，或 `MaxAge`/`MaxTotalBytes < 0` |
| `ErrInvalidRecord` | 时间戳为零、空数据、`Bytes <= 0`、`Bytes != len(Data)`、单条超段容量 |
| `ErrClockBackwards` | 追加/保留时间早于已接受的最大时间戳 |
| `ErrOutOfRange` | 读取起点越界/未对齐、`maxBytes <= 0` |

所有拒绝都发生在状态变更之前：非法输入不会改变段列表、总字节数或起始位点
（失败不留痕）。

## 判定日志

`SetDebugOutput(io.Writer)` 后，每次追加与保留都会打印逐步输入、段列表与
判定依据，例如 `RETAIN time seg=0 DELETE last=... cutoff=...`、
`RETAIN size seg=1 KEEP total=... limit=... STOP`。传 `nil` 关闭。

## 本地验证

```bash
# 常规测试
go test ./segmentlog

# 竞态检测 + 多次重复（并发用例）
go test -race -count=3 ./segmentlog

# 全量测试 / 覆盖率 / 静态检查 / 格式
go test ./...
go test -coverprofile=coverage.out ./segmentlog
go tool cover -func=coverage.out
go vet ./...
gofmt -l .
```

测试覆盖：整段删除、时间边界（cutoff 相等保留）、大小边界（恰好等于上限
停止、活动段保留）、滚动边界（恰好填满不滚动、等容量记录开新段）、各类
非法输入与拒绝后状态不变、并发追加/保留/读取（`-race`），以及与独立书写
的朴素参照模型在随机操作序列下的逐字段一致性。
