# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 直接运行
go run ./cmd/server

# 编译后运行
go build -o bin/server ./cmd/server
./bin/server
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## Modbus RTU 接收状态机（`modbusrtu` 包）

带注入时钟的 Modbus RTU 帧接收器，完全由调用时刻驱动，无内部定时器，
因此帧边界与各类丢帧原因可精确复现。

### 构造

`New(baud int, addr int) (*Receiver, error)`：

- `baud` 必须为正；`addr`（本机从站地址 S）必须在 1..247，否则构造失败。
- 字符时间按 11 位计。设 B 为波特率：
  - `B >= 19200`：t1.5 固定 `750000 ns`（750µs），t3.5 固定 `1750000 ns`（1750µs）。
  - `B < 19200`：`t15 = floor(33×10^9 / (2B)) ns`，
    `t35 = floor(77×10^9 / (2B)) ns`。

### 接口

- `OnByte(b byte, t time.Time) (*Event, error)`：在时刻 `t` 收到一个字节。
- `Poll(t time.Time) (*Event, error)`：只推进时间，不接收字节。
- `Counters() Counters`：返回 `Delivered/Ignored/IntraGap/TooLong/TooShort/CRCErrors` 计数快照。

每次调用最多结算出一个事件（交付或丢帧），随该次调用返回。

### 间隔判定（g = 与上一字节的间隔，丢弃态下被忽略的字节同样更新"上一字节"）

- `g >= t35`（左闭）：上一帧立即结束并结算，本字节开启新帧；
  若上一帧已作废（丢弃态），则不重复结算，仅静默退出丢弃态。
- `t15 < g < t35`（t15 本身不算超时）：帧内间隔违规，当前帧作废、
  立即返回 `ErrIntraFrameGap`，随后进入丢弃态；该字节被忽略。
- 其余情况（含 `g == t15`）：字节追加到当前帧。
- 丢弃态下所有字节被忽略，直到出现 `g >= t35` 的间隔才退出。
- `Poll` 时距上一字节已 `>= t35` 即结算当前帧；丢弃态下只静默复位。
- 帧在第 **257** 个字节到达时立即作废（`ErrFrameTooLong`）并进入丢弃态。

### 帧结算顺序（只报第一个）

1. 长度 `< 4`：`ErrFrameTooShort`；
2. CRC 不符：`ErrCRC`；
3. 地址既不是 S 也不是广播地址 0：不交付，计入 `Ignored`
   （`Event.Ignored == true`，不是错误）。

地址为 S 或 0 且长度、CRC 均合格的帧交付（`Event.Delivered == true`）。

### CRC

CRC-16/MODBUS：初值 `0xFFFF`，反射多项式 `0xA001`；
校验串 `"123456789"` 的值为 `0x4B37`。
线上低字节在前（`[crcLo, crcHi]`），计算范围为 CRC 字段之前的全部字节。

### 时钟与并发

- 调用时刻早于上一次接受调用的时刻时返回 `ErrClockBackward`，
  被拒绝的调用不改变任何状态与计数；时刻相等允许。
- 丢帧原因 `ErrIntraFrameGap / ErrFrameTooLong / ErrFrameTooShort / ErrCRC`
  均可用 `errors.Is` 区分，并各有独立计数。
- `OnByte`、`Poll`、`Counters` 内部互斥，并发调用等价于某个串行顺序；
  相同的字节与时刻序列重放得到完全相同的事件序列与计数。

### 本地验证

```bash
# 全量用例（含竞态检测）
GOCACHE=/tmp/go-cache go test -race -v ./modbusrtu

# 朴素模拟对照（300 组随机场景，日志打印输入、输出与逐条判定依据）
GOCACHE=/tmp/go-cache go test -race -run TestDifferentialAgainstNaiveSimulator -v ./modbusrtu

go vet ./...
gofmt -l .
```

覆盖点：间隔恰为 t15 / t15+1ns / t35-1ns / 恰为 t35；
波特率 19200 与 19199 的阈值公式切换；帧长 3、4、256、257；
广播地址 0、本机地址与他站地址的差异；`Poll` 单独结算一帧；
时钟回退不改变状态；并发安全；与逐事件朴素模拟器（`sim_test.go`）的随机对照。
