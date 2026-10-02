# QUIC 式丢包检测与探测超时控制器

包 `quicloss` 实现 RFC 9002 风格的丢包检测与 PTO（Probe Timeout）
控制器，包含两个包号空间：`H`（握手，`SpaceHandshake`）与
`A`（应用，`SpaceApp`）。所有时间单位为毫秒，取值范围
`0..1_000_000_000_000`。

## 状态

- 全局：`latest`、`srtt`（初值 333）、`rttvar`（初值 166）、
  `minRtt`（初值空）、`hasSample`（初值假）、`ptoCount`（初值 0）、
  `confirmed`（初值假）。
- 每空间：未确认报文表（包号 `pn`、发送时刻、大小、是否触发确认
  `ae`）、已见最大被确认包号 `la`（初值空）、丢包时刻 `lt`（初值空）、
  该空间最大已发送包号、最近 `ae` 报文发送时刻 `lastAe`。

## 操作

- `New(maxAckDelay)`：`maxAckDelay ∈ [0,10000]`。
- `Send(now, space, pn, size, ae)`：`pn` 必须严格大于该空间此前最大
  发送包号（首个包号为任意非负数），`size ∈ [1,65535]`。
- `Ack(now, space, pns, ackDelay)`：`pns` 非空、不含负数、每个元素不
  大于该空间最大已发送号；已不在表中的号（已确认、已判丢、或在发送
  范围内但从未登记）按重复忽略。
- `Detect(space, now)`：对单空间做丢包检测，返回按包号升序的丢包列表。
- `Timer()`：返回下一次触发 `(时刻, 模式, 空间)` 或空。
- `OnTimeout(now)`：只处理一个定时器。
- `HandshakeConfirmed(now)`：确认握手、丢弃 `H`、`ptoCount=0`。

所有方法互斥串行化（单一 mutex），并发调用等价于某个串行顺序；
重放同一被接受调用序列可得到完全相同的丢包、定时器与探测请求。

## RTT 更新次序

仅当本次“新确认报文里的最大包号 `L`”的 `ae` 为真时更新：

1. `latest = max(now − L.send, 1)`；
   `minRtt = min(minRtt, latest)`（首次即 `latest`）。
2. 首个样本：`srtt = latest`，`rttvar = ⌊latest/2⌋`，`hasSample=true`。
3. 之后先算 `ad`，再算 `adj`，然后**先用更新前的 `srtt`** 更新
   `rttvar`，最后更新 `srtt`：
   - `ad = min(ackDelay, maxAckDelay)`，但仅当 `space == A` 且
     `confirmed == true` 时取用；`H` 空间或确认前一律 `ad = 0`；
   - `adj = latest`（当 `latest < minRtt + ad`），否则
     `adj = latest − ad`；
   - `rttvar = ⌊(3·rttvar + |srtt − adj|) / 4⌋`；
   - `srtt   = ⌊(7·srtt + adj) / 8⌋`。

例：首样本 `latest=100 → srtt=100, rttvar=50`；确认后
`latest=140, ackDelay=10, maxAckDelay=25 → adj=130 →
rttvar=⌊(150+30)/4⌋=45, srtt=⌊830/8⌋=103`；再
`latest=105, ackDelay=10`，因 `105 < 100+10` 不扣延迟，`adj=105 →
rttvar=⌊(135+2)/4⌋=34, srtt=⌊826/8⌋=103`。

## 丢包判定（取等语义）

每次检测先令 `lt` 为空；`la` 为空则结束。

```
loss_delay = max(⌊9·max(latest, srtt)/8⌋, 1)
```

对表中所有 `pn < la` 的报文，满足任一条件即判丢并移除：

- 包数阈值：`la − pn >= 3`；
- 时间阈值：`sendTime <= now − loss_delay`（**取等即丢**）。

都不满足则 `lt = min(lt, sendTime + lossDelay)`。`pn > la` 的报文既不
判丢也不参与 `lt`。返回列表按包号升序。

例：A 空间 pn 1..5 分别在 0,10,20,399,400 发送，`now=420` 确认 `{5}`，
`latest=20`，`loss_delay=22`：pn 1,2 由包数阈值判丢，pn 3 由
`20 <= 398` 判丢；pn 4 因 `399 > 398` 保留且 `lt=421`。`now=421`
触发时 `399 <= 421−22`（取等）判丢 pn 4。

## 定时器选择与退避

1. 若任一空间 `lt` 非空，取最小者，模式 `Loss`；并列取 `H`。
2. 否则对“表中含 `ae` 报文”的空间计算 PTO：
   - `A` 空间仅在 `confirmed` 之后参与；`H` 在丢弃后不参与；
   - `pto = srtt + max(4·rttvar, 1) + (A 空间 ? maxAckDelay : 0)`；
   - 时刻 `= lastAe + pto · 2^min(ptoCount,20)`；
   取最小者，模式 `PTO`；并列取 `H`。
3. 都不满足则无定时器。

Loss 始终优先于 PTO。`OnTimeout`：

- 时刻未到或无定时器 → `ErrTimeoutEarly`（触发过早）；
- Loss：对该空间执行 `Detect`，返回升序丢包列表；
- PTO：`ptoCount++`，返回对应空间探测请求，不判任何丢包。

例（`maxAckDelay=25`，`srtt=103, rttvar=34, lastAe=1000`）：
`pto = 103+136+25 = 264`，定时器 `1264,PTO`；触发后 `ptoCount=1`，
1264 再发一个 `ae` 报文后定时器为 `1264 + 264·2 = 1792`。

## 清零与忽略规则

- 一次 ACK 中只要有任意“新确认且 `ae` 为真”的报文，`ptoCount` 清零；
  `newly` 为空（全重复）的确认不改变时钟以外的任何状态，因此不清零。
- `ackDelay` 在 `H` 空间、或 A 空间确认前被忽略。

## 空间丢弃语义

`HandshakeConfirmed` 之后：`confirmed=true`、`H` 的报文与 `la/lt` 等
全部清空（不判丢、不产生丢包列表）、`ptoCount=0`。此后对 `H` 的
`Send/Ack/Detect` 一律返回 `ErrSpaceDiscarded`；A 空间从此参与 PTO。
再次确认是幂等的。

## 错误原因（按校验顺序，只报第一个）

1. 空间名非法 `ErrInvalidSpace`
2. `size` 越界 `ErrInvalidSize`
3. 时刻越界 `ErrInvalidTime`
4. 包号为负 `ErrInvalidPN`（ACK 还包括集合为空 `ErrEmptyAck`、
   `ackDelay` 非法 `ErrInvalidAckDelay`）
5. 时钟回退 `ErrClockBackward`
6. 空间已丢弃 `ErrSpaceDiscarded`
7. 包号非递增 `ErrPNNotIncreasing`
8. 确认了从未发送的包号 `ErrAckedNeverSent`
9. 触发过早 `ErrTimeoutEarly`

被拒绝的调用不改变任何状态，包括时钟（`lastNow` 不前进）。

## 本地验证

```bash
go test ./...
go test -race -count=1 ./...
go test -v -run TestSpec ./quicloss
go test -v -run TestDifferentialRandom ./quicloss   # 2000 组随机对照
go vet ./...
gofmt -l .
```

`TestDifferentialRandom` 将 2000 组随机调用序列（含各类非法调用与
边界时刻）同时送入生产 `Controller` 与按规范逐行书写的朴素参考模型
`refModel`，逐步比对返回值、错误原因、丢包列表、定时器、探测请求以及
完整内部状态；`-v` 日志逐条打印输入、输出与判定依据（Loss/PTO/拒绝
原因）。
