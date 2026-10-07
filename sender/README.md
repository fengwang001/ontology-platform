# sender — 传输层发送端小段合并调度器

`sender` 实现一个可精确复现的发送端调度器：在**应用写入、对端确认与
窗口通告、选项变更、时间推进、关闭**五类事件的驱动下，决定何时把缓冲
数据切成段发出。时钟由调用方注入（每个事件携带当前时刻），因此软木塞
超时与零窗口探测的相互作用可以离线逐拍重放。

## 快速开始

```go
cfg := sender.Config{
    MSS:           1460,
    InitialWindow: 65535,
    BufferCap:     1 << 20,
    CorkTimeout:   100 * time.Millisecond,
    ProbeInterval: 500 * time.Millisecond,
}
s, err := sender.NewScheduler(cfg)
if err != nil { /* 配置非法 */ }

segs, err := s.Write(t0, 4096)        // 应用写入
segs, err  = s.Ack(t1, 1460, 65535)   // 对端确认 + 窗口通告
segs, err  = s.SetNoDelay(t2, true)   // 选项变更
segs, err  = s.Advance(t3)            // 时间推进（触发超时/探测）
segs, err  = s.Close(t4)              // 关闭发送端
```

每个事件方法返回本次发出的各段（`[]Segment{Len, At}`）。被拒绝的事件
返回哨兵错误且不改变任何状态。

## 事件与查询

| 方法 | 事件 | 可能的错误 |
| --- | --- | --- |
| `Write(now, n)` | 应用写入 n 字节 | `ErrInvalidParam` `ErrClockBackward` `ErrWriteAfterClose` `ErrBufferFull` |
| `Ack(now, acked, window)` | 确认 acked 字节 + 通告窗口 | `ErrInvalidParam` `ErrClockBackward` `ErrWindowShrink` `ErrAckOutOfRange` |
| `SetNoDelay(now, on)` | 设置/清除不延迟选项 | `ErrClockBackward` |
| `SetCork(now, on)` | 设置/清除软木塞选项 | `ErrClockBackward` |
| `Advance(now)` | 推进时钟 | `ErrClockBackward` |
| `Close(now)` | 关闭发送端 | `ErrClockBackward` |

查询（只读，不改变状态，可在任意时刻调用）：

- `InFlight()` 在途字节数（已发送未确认）。
- `Buffered()` 缓冲中尚未发出的字节数。
- `Retained()` 当前被保留规则扣留的小段字节数（窗口为零时为零）。
- `NextDeadline()` 下一个仅因时间推进就会触发动作的时刻
  （软木塞到期或探测到期的较早者）。

错误判定优先级固定：`ErrInvalidParam` > `ErrClockBackward` >
`ErrWriteAfterClose` > `ErrBufferFull` > `ErrWindowShrink` >
`ErrAckOutOfRange`。

## 发送规则速览

- 可发送上限 = 对端窗口右边缘 − 已发送序号；右边缘只增不减。
- 满段（长度恰为 MSS）在窗口允许范围内总是可发；尾部不足一个满段的
  数据、以及被窗口截短的段，都是**小段**。
- 默认模式：在途为空时小段立即发出；在途非空时保留，直到在途全部
  被确认或凑满满段。
- 不延迟选项：小段只受窗口约束。软木塞选项：只发满段，小段保留至
  超时。两者同时打开时软木塞优先。
- 关闭后忽略一切保留约束，仅按窗口约束排空缓冲。
- 窗口为零且缓冲非空、在途为空时，按探测间隔发出一字节探测段。

完整的语义定义、锚点规则与取舍见 [DESIGN.md](DESIGN.md)。

## 本地验证

```bash
go test ./sender/                 # 确定性用例 + 1200 组随机对照
go test -race ./sender/           # 并发串行化验证
go test -v ./sender/ -run TestAgainstNaiveModel/seed=7   # 查看逐步日志
go test ./sender/ -run xxx -bench . -benchmem            # 开销基准
```
