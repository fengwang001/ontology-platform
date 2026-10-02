# quicloss — RFC 9002 风格丢包检测与 PTO 定时器

`quicloss.Controller` 在两个包号空间 **H（握手）** 与 **A（应用/1-RTT）** 上
维护未确认报文、往返时延估计与定时器。所有时刻单位为毫秒，取值范围
`[0, 10^12]`。所有公开方法由同一把互斥锁串行化，因此并发调用等价于某个确定
的串行顺序；重放相同的已接受调用序列，得到的丢包列表、`Timer()` 结果与探测
请求完全一致。

初始共享状态：`latest = srtt = 333`、`rttvar = 166`、`minRtt` 为空、
`hasSample = false`、`ptoCount = 0`、`confirmed = false`。每空间的 `la`
（最大被确认包号）与 `lt`（丢包重估时刻）初始为空。

## 操作与拒绝原因

- `New(maxAckDelay)`：`maxAckDelay` 必须在 `[0,10000]`，否则
  `ErrInvalidMaxAckDelay`。
- `Send(now, space, pn, size, ae)`：`size ∈ [1,65535]`；空间首包号可为任意
  非负数，之后必须严格大于该空间此前的最大发送包号。
- `Ack(now, space, pns, ackDelay)`：`pns` 必须非空且不含负数；每个号不得大于
  该空间已发送的最大包号（`ErrPNNeverSent`）。号不在未确认表中（已确认、已判
  丢，或是合法号段内从未登记的空洞）视为重复而忽略；若 `newly` 为空，则除
  “接受时钟”外不改变任何状态（不清零 `ptoCount`、不更新 RTT、不动 `la`）。
- `Detect(space, now)`、`OnTimeout(now)`、`HandshakeConfirmed(now)` 同样校验
  时刻并受单调时钟约束。

错误按下述顺序只报第一个，被拒绝的调用不改变任何状态（包括时钟）：

1. 参数非法：空间名 → size → 时刻越界 →（Send 的）负包号；
   Ack 为 空间名 → 时刻越界 → 空集合 → 负包号。
2. 时钟回退 `ErrClockBackwards`：`now` 小于上次被接受调用的 `now`。
3. 空间已丢弃 `ErrSpaceDiscarded`：`HandshakeConfirmed` 后再对 H 做
   `Send/Ack/Detect`。
4. 包号非递增 `ErrPNNotIncreasing`（Send）。
5. 确认了从未发送的包号 `ErrPNNeverSent`（Ack：超过该空间最大已发送号）。
6. 触发过早 `ErrTimeoutEarly`（`OnTimeout` 早于所选定时器时刻；无定时器时为
   `ErrNoTimer`）。

注意 `Timer()` 是无参纯查询：不校验时刻、不推进时钟。

## RTT 更新次序

`Ack` 仅当 `newly` 非空时推进状态，严格按以下次序：

1. `L = max(newly)`，`la = max(la, L)`。
2. 当且仅当 L 的 `ae = true` 产生 RTT 样本：
   - `latest = max(now - L.sentAt, 1)`；`minRtt = min(minRtt, latest)`，首次样本
     即把 `minRtt` 设为 `latest`。
   - 首次样本：`srtt = latest`、`rttvar = ⌊latest/2⌋`、`hasSample = true`。
   - 后续样本：仅当 `space == A && confirmed` 才取 `ad = min(ackDelay,
     maxAckDelay)`（负值钳为 0），否则 `ad = 0`；当
     `latest < minRtt + ad` 时 `adj = latest`（不扣延迟），否则
     `adj = latest - ad`。随后用**更新前**的 `srtt` 计算
     `rttvar = ⌊(3*rttvar + |srtt - adj|)/4⌋`，再
     `srtt = ⌊(7*srtt + adj)/8⌋`。
3. 从表中移除全部 `newly`；其中只要含一个 `ae` 报文就把 `ptoCount` 置 0。
4. 对该空间执行丢包检测。

## 丢包检测（包数阈值与时间阈值，均含等号）

`Detect` 先清空该空间 `lt`；`la` 为空则结束。

- `loss_delay = max(⌊9 * max(latest, srtt) / 8⌋, 1)`。
- 对所有 `pn < la` 的在表报文：
  - 若 `la - pn >= 3`（**取等即丢**），或 `sentAt <= now - loss_delay`
    （**取等即丢**），判丢并移除；
  - 否则记 `lt = min(lt, sentAt + loss_delay)`。
- `pn >= la` 的报文既不判丢也不参与 `lt`。
- 返回的丢包列表按包号升序。

例：A 空间 pn 1..5 发送于 0、10、20、399、400（均 `ae`），`now=420` 确认
`{5}`：样本 20，`srtt=20`，`loss_delay=⌊9*20/8⌋=22`；pn1、pn2 由包数阈值
（差 4、3）判丢，pn3 由 `20 <= 420-22=398` 判丢，pn4 因 `399 > 398` 保留且
`lt=399+22=421`；`now=421` 触发时 `399 <= 421-22=399` 取等，pn4 判丢。

## 定时器选择与指数退避

`Timer()` 返回 `(TimerInfo, true)` 或无事件，确定地二选一：

1. **Loss 优先**：任一空间 `lt` 非空时，取所有 `lt` 的最小值；时刻并列时选
   H。模式为 `Loss`。
2. **否则 PTO**：对每个“表中含 `ae` 报文”的候选空间取
   `lastAe = 最大 ae 发送时刻`；A 空间仅在 `confirmed` 后参与（H 恒可参与）。
   - `pto = srtt + max(4*rttvar, 1) + (space == A ? maxAckDelay : 0)`
   - `time = lastAe + pto * 2^min(ptoCount,20)`
   - 取各候选最小值；并列选 H。模式为 `PTO`。
3. 两类候选都没有则无定时器。

`OnTimeout(now)` 一次只处理当前选出的一个定时器，并要求存在定时器且
`now >= 其时刻`：

- `Loss`：对该空间再跑一次 `Detect`，返回 `TimeoutResult.Loss`（可能为空）。
- `PTO`：`ptoCount += 1`，返回
  `TimeoutResult.Probe = &ProbeRequest{Space: 对应空间}`，不判任何丢包；下一次
  `Timer()` 自然采用加倍后的退避（上限移位 20）。

## 空间丢弃语义

`HandshakeConfirmed(now)`：置 `confirmed = true`，静默清空 H 全部报文与 `la/lt`
（不产生丢包事件），`ptoCount = 0`，并推进接受时钟。此后：

- A 空间开始参与 PTO 候选，且 A 的 ACK 允许计入 `ackDelay`；
- H 的 `Send/Ack/Detect/Outstanding` 一律返回 `ErrSpaceDiscarded`；
- 重复调用 `HandshakeConfirmed` 是幂等的（仍受时钟回退校验）。

## API 速览

- `New(maxAckDelay int64) (*Controller, error)`
- `Send(now int64, space byte, pn int64, size int, ae bool) error`
- `Ack(now int64, space byte, pns []int64, ackDelay int64) ([]int64, error)`
- `Detect(space byte, now int64) ([]int64, error)`
- `Timer() (TimerInfo, bool)`（字段 `Time/Space/Mode`，模式 `ModeLoss/ModePTO`）
- `OnTimeout(now int64) (*TimeoutResult, error)`
- `HandshakeConfirmed(now int64) error`
- 辅助只读检查：`Outstanding(space) ([]Packet, error)`、`RTT() Snapshot`。

## 本地验证

本仓库的 Go 工具链可能不在默认 `PATH`，构建缓存放 `/tmp`：

```bash
export PATH=$PATH:/usr/local/go/bin
export GOCACHE=/tmp/gocache

go test ./...
go test -race -v ./quicloss
go vet ./...
gofmt -l .
```

测试内容：

- `quicloss_test.go`：题目给定的丢包取等例、RTT 三步算术例（100/50 →
  140/adj130/45/103 → 105/不扣延迟/34/103）、PTO `1264` 与退避 `1792` 例、
  Loss 优先于 PTO 与 H 并列优先、confirmed 前 A 不参与 PTO、无新确认的 ACK
  不清零 `ptoCount`、H 或 confirmed 前 `ackDelay` 被忽略、空间丢弃、全部可
  区分错误与拒绝不改状态。
- `difftest_test.go`：`TestRandomDifferential` 用 2000 组、每组 60 个随机调用
  （Send/Ack/Detect/Timer/OnTimeout/HandshakeConfirmed，含各类非法输入与过早
  触发）对照一个独立编写的朴素逐步模拟，逐步比对全部内部状态；失配时打印该
  seed 的完整输入、输出与判定依据日志。设置 `QUICLOSS_DIFF_TRACE=1` 可在
  `-v` 模式下打印每组完整轨迹：

```bash
QUICLOSS_DIFF_TRACE=1 go test -run TestRandomDifferential -v ./quicloss
```

- `TestConcurrentSafety` 在 8 个 goroutine 并发下调用全部方法，配合 `-race`
  验证串行化。
