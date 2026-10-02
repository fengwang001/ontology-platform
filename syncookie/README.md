# SYN Cookie 握手验证器

`package syncookie` 实现一个带半开队列溢出切换的 SYN 握手验证器：
半开队列有空位时登记有状态握手项；半开队列满时改发**无状态 SYN Cookie**；
最终 ACK 到达时，先按半开队列项校验，未命中再按 Cookie 校验。
所有操作可并发调用，内部用互斥锁串行化，结果等价于某个合法的串行顺序。

## 构造

```go
func New(B int, T int64, A int, hash HashFunc, nextISN NextISNFunc) (*Validator, error)
```

- `B`：半开队列容量，`1..1024`。
- `T`：半开超时（毫秒），`1..1_000_000`。
- `A`：待取（accept）队列容量，`1..1024`。
- `hash`：`func(caddr, saddr uint32, cport, sport uint16, cisn, t uint32) uint32`。
- `nextISN`：`func() uint32`，每次有状态入队调用一次取新的服务端 ISN。

任一构造参数越界、`hash`/`nextISN` 为 `nil` 返回 `ErrInvalidParam`。

连接键 `Key{CAddr, CPort, SAddr, SPort}`；MSS 表固定为
`[536, 1220, 1460, 8960]`；tick `t = floor(now / 64000)`，`now` 单位毫秒、
范围 `0..10^12`；所有 32 位序号运算按模 2^32（Go 的 `uint32` 天然回绕）。

## Cookie 位布局

Cookie 编码在服务端 ISN（32 位）中：

```text
 31          27 26     24 23                       0
+-------------+---------+--------------------------+
| t5 = t % 32 | mi (3b) | h = Hash(...) & 0xFFFFFF |
+-------------+---------+--------------------------+
```

```text
ISN = (t % 32) << 27 | mi << 24 | (Hash(caddr, saddr, cport, sport, cisn, t) & 0xFFFFFF)
```

- 高 5 位：签发时的 tick 低 5 位 `t5`。
- 中 3 位：协商 MSS 在固定表中的下标 `mi`（0..3）。
- 低 24 位：注入哈希的低 24 位。

校验时令 `c = ack - 1`，取 `t5 = c >> 27`、`mi = (c >> 24) & 7`、
`h = c & 0xFFFFFF`。

## MSS 向下取表规则

`mi` 为表中**不超过**通告 `mss` 的最大项下标；`mss` 小于最小项 536 时
取 `mi = 0`（对应 536）。例如：

| mss  | 500 | 535 | 536 | 1219 | 1220 | 1459 | 1460 | 8959 | 8960 | 65535 |
| ---- | --- | --- | --- | ---- | ---- | ---- | ---- | ---- | ---- | ----- |
| mi   | 0   | 0   | 0   | 0    | 1    | 1    | 2    | 2    | 3    | 3     |

## 过期与有效期边界

- 半开项：`created + T <= now` 即过期（**恰等也过期**），在每个被接受
  操作开头清理；`134999` 仍存活、`135000` 已过期（T=5000、created=130000）。
- Cookie：`age = (t - t5) mod 32`（取 0..31 的非负余数，自动处理 tick
  回绕）。`age <= 1` 有效，`age >= 2` 为 `ErrCookieExpired`。
  即签发 tick 与当前 tick 相同（age=0）或上一个 tick（age=1）可用。
- 回绕例：当前 `t=32`（字段回绕为 0）时，stamp=32（age=0）、
  stamp=31（age=1）通过；stamp=30（age=2）过期。
- 重算哈希使用 `t0 = t - age`（uint32 回绕），即恢复签发 tick。

## OnSyn 判定次序

前置（这些失败的操作属于被拒绝操作：不做任何清理）：

1. `now < 0 || now > 10^12` -> `ErrInvalidTime`。
2. `now` 小于上一次被接受操作的 `now` -> `ErrClockBack`。
3. `mss > 65535` -> `ErrInvalidMSS`。

通过后先清理过期半开项、更新时钟，再按序：

1. key 已在半开队列：重传，返回原 `sisn`、`Cookie=false`，`Retrans+1`；
   不新建、不刷新 `created`。
2. 待取队列已满（数量等于 A）：丢弃，`ErrAcceptFull`。
3. 半开数 < B：入队 `{sisn=NextISN(), cisn, mss, created=now}`，
   返回 `{ISN: sisn, Cookie:false}`。
4. 否则发 Cookie（不存任何状态），`CookieSent+1`。

## OnAck 两条路径与判定次序

先通过同样的时间/回退前置，再清理过期半开项、更新时钟。

1. **半开路径**（key 在半开队列）：
   - `ack != sisn+1` 或 `seq != cisn+1` -> `ErrBadAck`，**项保留**。
   - 否则待取队列满 -> `ErrAcceptFull`，**项保留**。
   - 否则移出半开队列，按记录的 `mss` 入待取队列，返回协商 `mss`。
2. **Cookie 路径**（key 不在半开队列），按序只报第一个原因：
   - `mi >= 4` -> `ErrCookie`。
   - `age > 1` -> `ErrCookieExpired`。
   - `Hash(caddr, saddr, cport, sport, seq-1, t0) & 0xFFFFFF != h`
     -> `ErrCookie`。
   - 待取队列满 -> `ErrAcceptFull`。
   - 否则按 `MSS[mi]` 入待取队列，`CookieOK+1`，返回协商 `mss`。

`Accept()` 按完成先后（FIFO）取出 `(key, mss)`，空队列返回 `ErrEmpty`。

## 容量拒绝与计数口径

- 任何时刻半开数 `<= B`、待取数 `<= A`；每个 Established 的连接恰好入
  待取队列一次（半开路径移出原项，Cookie 路径此前无任何状态）。
- `CookieSent`：只有 OnSyn 走到分支（四）才 +1。
- `CookieOK`：只有 Cookie 路径通过全部校验并入队才 +1；
  `ErrCookie`/`ErrCookieExpired`/`ErrAcceptFull` 均不计。
- `Retrans`：只有命中现存半开 key 的 SYN 才 +1。
- `Stats().HalfOpen` 为**最近一次被接受操作清理后**的半开数；
  `Stats()` 本身不推进时钟、不清理。
- 被拒绝操作（非法时间/回退/越界 mss）不改队列、统计与时钟，也不清理；
  过期项要等到随后的被接受操作开头才被移除。注意：因队列满而返回
  `ErrAcceptFull` 的 SYN/ACK 属于**被接受操作**（输入合法），仍会先清理。

## 可重放性

相同的操作序列与相同的注入函数（`hash`、`nextISN`）重放，得到完全相同的
响应与统计。`nextISN` 的调用次数与顺序由规范固定（每次有状态入队一次，
Cookie 路径不调用），因此建议注入函数自身确定、无外部状态。

## 本地验证

```bash
# 全量测试（含 2000 组随机序列对朴素模拟的差分对照）
go test ./...

# 竞态检测
go test -race ./...

# 查看差分日志：逐条打印输入、输出与判定依据
go test -run TestNaiveDifferential -v

# 规范点名单用例
go test -run 'TestRequiredScenario|TestAcceptFullBothPaths|TestBadAckRetainsEntry|TestRejectedOpSkipsCleanup|TestInvalidArgsAndRollback' -v
```

测试构成：

- `scenario_test.go`：规范点名的全部例子（B=1/T=5000、t=2、mi 边界、
  重传不刷新、恰等过期、age=1 通过 / age=2 过期、t5 回绕、坏哈希、
  mi 越界）。
- `queue_test.go`：两条 ACK 路径在待取队列满时的 `ErrAcceptFull` 与
  项保留、FIFO、`ErrEmpty`、`ErrBadAck` 保留、32 位回绕、被拒绝不清理、
  参数与时钟校验。
- `naive_model_test.go`：按规范逐行写成的朴素参考模拟器。
- `diff_test.go`：2000 组随机操作序列在实现与朴素模拟器之间逐步对照，
  日志打印 `IN/OUT/判定依据`；`-race` 下自动减为 30 组。
- `concurrent_test.go`：多 goroutine 并发压测，校验 `HalfOpen<=B`、
  `Acceptable<=A` 与“恰好入队一次”。
