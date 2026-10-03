# syncookie — 带半开队列溢出切换的 SYN Cookie 握手验证器

`syncookie` 在半开队列有空位时登记有状态半开项，队列满时切换为无状态
SYN Cookie；ACK 到来时分别按队列项与 Cookie 两条路径校验，校验通过的
连接按完成先后进入待取（accept）队列。所有 32 位序号运算按模 2^32。

## 构造参数

```go
v, err := syncookie.New(B, T, A, hash, nextISN)
```

- `B`：半开队列容量，1..1024。
- `T`：半开超时（毫秒），1..1e6。
- `A`：待取队列容量，1..1024。
- `hash`：`func(caddr, saddr uint32, cport, sport uint16, cisn, t uint32) uint32`，取低 24 位参与 Cookie。
- `nextISN`：`func() uint32`，半开路径的服务端 ISN 发生器。

连接键 `Key` 为 `(CAddr, CPort, SAddr, SPort)`。tick `t = now / 64000`（`now` 为毫秒，合法范围 0..1e12）。

## Cookie 位布局

Cookie 即 SYN-ACK 中的服务端 ISN，32 位：

- 位 31..27：`t % 32`（发 Cookie 时的 tick，5 位，每 32 个 tick 回绕）。
- 位 26..24：`mi`，MSS 表下标（编码时 0..3；解码按 3 位读取，4..7 判 `ErrCookie`）。
- 位 23..0：`Hash(caddr, saddr, cport, sport, cisn, t) & 0xFFFFFF`。

即 `ISN = (t%32)<<27 | mi<<24 | Hash(...)&0xFFFFFF`。

## MSS 向下取表规则

MSS 表固定为 `[536, 1220, 1460, 8960]`。`mi` 取表中不超过客户端
`mss` 的最大项的下标；`mss < 536` 时取 0。例如 `mss` 为
500、1459、1460、8960 时，`mi` 依次为 0、1、2、3。

## 过期与有效期边界

- 每个被接受的操作（通过参数与时钟校验）开头先清理半开项：
  `created + T <= now` 即过期（恰等也算过期），清理后才进入业务判定。
  被拒绝的操作不清理、不改变任何状态、不推进时钟。
- 半开重传（key 已在半开队列）返回原 `sisn` 并使 `retrans` 加 1，
  不新建项、不刷新 `created`。
- Cookie 有效期按 tick 计：解码得 `t5 = c>>27`，
  `age = (t%32 - t5) mod 32`（非负）；`age` 为 0 或 1 时有效，
  `age > 1` 判 `ErrCookieExpired`。tick 每 32 个回绕，因此 t=31 签发
  的 Cookie 在 t=32（age=1）仍有效，t=33（age=2）过期。
- 校验哈希时使用 `t0 = t - age`（uint32 回绕）重算。

## 操作判定次序

所有操作先做参数与时钟校验（`ErrInvalidNow`、`ErrInvalidMSS`、
`ErrClockBackwards`，可区分且先于其余原因；`now` 范围先于时钟回退
判定），通过后清理过期半开项，再按下列次序只报第一个命中的原因。

`OnSyn(now, key, cisn, mss)`：

1. key 已在半开队列：重传，返回原 `sisn`，`retrans++`。
2. 待取队列已满（数量等于 A）：`ErrAcceptFull`。
3. 半开数小于 B：入队 `{sisn=NextISN(), cisn, mss, created=now}`。
4. 否则发无状态 Cookie（不存任何状态），`cookieSent++`。

`OnAck(now, key, seq, ack)`：

- 半开路径（key 在半开队列）：`ack != sisn+1 || seq != cisn+1` 判
  `ErrBadAck`（项保留）；待取队列满判 `ErrAcceptFull`（项保留）；
  否则移出半开、按协商 `mss` 入待取队列，连接建立。
- Cookie 路径（`c = ack - 1`）：依次判定 `mi >= 4` → `ErrCookie`；
  `age > 1` → `ErrCookieExpired`；
  `Hash(caddr, saddr, cport, sport, seq-1, t0)&0xFFFFFF != c&0xFFFFFF`
  → `ErrCookie`；待取队列满 → `ErrAcceptFull`；否则按
  `mss = MSS表[mi]` 入待取队列，`cookieOK++`，连接建立。

`Accept()` 取出最早入待取队列者 `(key, mss)`，空队列判 `ErrEmpty`。

## 容量拒绝与计数口径

- 任意时刻半开数不超过 B、待取数不超过 A；每个 Established 连接恰好
  入待取队列一次。
- `cookieSent` 只在实际发出 Cookie（`OnSyn` 第 4 分支）时加 1；
  `cookieOK` 只在 Cookie 校验通过并成功入待取队列时加 1；
  `retrans` 只在命中半开重传时加 1。
- 被拒绝的操作（参数非法、时钟回退）不改变半开队列、待取队列、
  统计与时钟，也不做清理；过期项由随后的被接受操作移除。
- `Stats()` 返回 `{HalfOpen, Pending, CookieSent, CookieOK, Retrans}`，
  其中 `HalfOpen` 为最近一次被接受操作清理后的值。
- 所有方法可并发调用（内部互斥），结果等价于某个串行顺序；相同操作
  序列与相同注入函数重放得到完全相同的响应与统计。

## 本地验证

```bash
# 全量测试（含 2000 组随机序列与朴素模拟对照）
go test ./syncookie/

# 打印每条操作的输入、输出与判定依据日志
go test ./syncookie/ -run TestRandomSequencesAgainstNaive -v

# 竞态检测
go test -race ./syncookie/

# 检查
gofmt -l . && go vet ./...
```
