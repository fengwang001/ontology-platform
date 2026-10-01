# modbusrtu —— 带注入时钟的 Modbus RTU 帧接收状态机

`modbusrtu.Receiver` 按字节时刻（`OnByte(byte, time.Time)`）或仅推进时间
（`Poll(time.Time)`）重组 Modbus RTU 帧，并以**每个调用最多一个事件**
（交付 / 忽略 / 丢帧）的形式返回结果。时刻由调用方注入，同一字节与时刻序列
重放得到完全一致的事件序列与计数；`OnByte`、`Poll`、`Snapshot` 可并发调用。

## 构造

`New(baud int, slaveAddr byte)`：

- `baud` 必须为正，否则返回 `ErrInvalidBaud`；
- `slaveAddr` 必须在 `1..247`，否则返回 `ErrInvalidSlaveAddr`。

字符时间按 **11 位/字符** 计算（纳秒）：

| 条件 | t1.5 | t3.5 |
| --- | --- | --- |
| `baud >= 19200` | 固定 `750000` | 固定 `1750000` |
| `baud < 19200` | `floor(33·10^9 / (2·baud))` | `floor(77·10^9 / (2·baud))` |

阈值可通过 `Thresholds()` 读取（例如 B=19199 与 B=19200 的公式切换点）。

## 间隔切帧规则

设与**上一个收到的字节**（含丢弃态下被忽略的字节；`Poll` 不更新该时刻）
的间隔为 `g`：

1. `g >= t3.5`：间隔左闭，先结算上一帧，再用本字节开启新帧；
2. `g > t1.5 且 g < t3.5`：帧内间隔违规，当前帧立即作废并进入丢弃态，
   本次调用返回 `ErrIntraGap`；`g == t1.5` 本身**不算**违规；
3. 其他情况字节追加到当前帧。

丢弃态下所有字节一律忽略，但仍参与间隔计算；只有再次出现 `g >= t3.5`
才能离开丢弃态（离开时被丢弃的残帧不再结算，因为其丢帧原因已在进入
丢弃态时报过）。

`Poll(t)`：若距上一字节 `>= t3.5`，按同样规则结算当前帧；丢弃态下则仅
清除丢弃态、不产生事件。未达 t3.5 或尚无字节时无事件。

帧在**第 257 个字节到达的当次调用**立即作废，返回 `ErrFrameTooLong`
（256 字节为合法上限，含两个 CRC 字节）。

## 帧结算顺序

结算严格按以下优先级，**只报第一个命中的原因**：

1. 长度 `< 4` → `ErrFrameTooShort`（恰为 4 为最短合法帧）；
2. CRC 不符 → `ErrCRC`；
3. 地址既不是本机地址 `S` 也不是广播 `0` → 不交付，计入 `Ignored`。

长度与 CRC 均合格时，地址为 `S` 或广播 `0` 才交付（`Delivered`）。

## CRC 参数

CRC-16/MODBUS：初值 `0xFFFF`，反射多项式 `0xA001`，无输入/输出反射外的
额外异或（算法内部按位反射处理），校验值 `CRC("123456789") == 0x4B37`。
帧内 CRC 字段**低字节在前**，计算范围为 CRC 字段之前的全部字节。

## 事件、错误与计数

- `Event.Kind`：`EventDelivered` / `EventIgnored` / `EventDropped`；
  无事件时调用返回 `nil`。`Event.Frame` 为独立拷贝，`Event.Time` 为
  触发结算的调用时刻。
- 丢帧原因用哨兵错误表示，支持 `errors.Is`：`ErrIntraGap`、
  `ErrFrameTooLong`、`ErrFrameTooShort`、`ErrCRC`。
- 时刻早于上一次任一调用（`OnByte`/`Poll`）返回 `ErrClockBackward`，
  被拒绝的调用不改变任何状态与计数；相等时刻合法。
- `Snapshot()` 返回 `Counters{Delivered, Ignored, IntraGap, TooLong,
  TooShort, CRCErrors}` 的瞬时副本。

## 本地验证

```bash
# 若 go 不在 PATH
export PATH=$PATH:/usr/local/go/bin
export GOCACHE=/tmp/gocache GOPATH=/tmp/gopath

go test -v ./modbusrtu                 # 详细日志：输入、输出事件与判定依据
go test -race ./modbusrtu              # 并发安全
go test -run TestNaiveSimulation -v    # 与逐事件朴素模拟器对照
go vet ./...
gofmt -l .
```

测试覆盖：间隔恰为 t1.5 / t1.5+1ns / t3.5-1ns / t3.5；B=19200 与 19199
的阈值公式切换；帧长 3/4 与 256/257；广播地址 0 与他站地址差异；`Poll`
单独结算；时钟回退不改变状态；400 组随机序列与朴素模型逐事件、逐计数
对照；同一序列两次重放结果完全一致。
