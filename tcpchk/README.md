# tcpchk：RFC 5961 风格 TCP 已建立连接段校验器

`tcpchk` 对单条已建立连接上的每个到达段做合法性判定，产出动作
（`Drop` / `AckPlain` / `AckChallenge` / `Suppressed` / `Reset` /
`Accepted`），并用全局挑战 ACK 限速器约束挑战应答。所有序号运算按
模 2^32 进行，位置一律用相对偏移 `(x−基准) mod 2^32` 比较：偏移
`< 2^31` 视为在基准右侧（或重合），`>= 2^31` 视为在左侧。

## 判定次序

`Process(now, seg)` 先做拒绝检查（拒绝的调用不改变任何状态，含时钟
与限速器），按以下顺序只报第一个原因：

1. `ErrInvalidParam`：`now > 10^12`、`Len/Wnd > 2^30`、`SYN` 与
   `RST` 同时为真（构造参数越界在 `NewValidator` 时报同类错误）；
2. `ErrClockBackwards`：`now` 小于上次被接受调用的 `now`；
3. `ErrAlreadyClosed`：连接已 `Closed`。

通过拒绝检查后，按固定次序判定（`L = Len + SYN + FIN`，SYN/FIN 各占
1 个序号）：

1. **RST 段（三分判定）**：忽略负载，按 `L=0` 判 `acc(Seq,0)`——
   不可接受则 `Drop`（静默）；`Seq == rcvNxt` 则 `Reset` 且连接变
   `Closed`；在窗口内但不等于 `rcvNxt` 则挑战（`fromRst=true`）。
2. **序号窗口**：`acc(Seq, L)` 不成立则 `AckPlain`（普通应答，不用
   限速器，不改任何序号）。
3. **SYN**：已可接受的 SYN 段走挑战（`fromRst=false`）。
4. **ACK 标志**：未置 ACK 则 `Drop`。
5. **ACK 号范围**：`lo = sndUna − maxSndWnd`，可接受当且仅当
   `(Ack−lo) mod 2^32 <= (sndNxt−lo) mod 2^32`，否则挑战。
6. **通过**：`Ack ∈ (sndUna, sndNxt]` 时 `sndUna = Ack`；
   `maxSndWnd = max(maxSndWnd, Wnd)`；`Seq` 在 `rcvNxt` 或其左侧且
   `Seq+L` 超过 `rcvNxt` 时 `rcvNxt = Seq+L`（左侧重叠被裁掉，窗口
   内乱序段不推进）；返回 `Accepted`。

除 `Reset`（只改连接状态）外，只有 `Accepted` 会改变序号状态。

## 可接受判定 acc(seq, L)

- `L=0`：`rcvWnd=0` 时要求 `seq == rcvNxt`，否则要求
  `(seq−rcvNxt) mod 2^32 < rcvWnd`；
- `L>0`：`rcvWnd=0` 时不可接受，否则首字节 `seq` 或末字节
  `seq+L−1` 的偏移 `< rcvWnd` 即可（偏移 `>= 2^31` 落在 `rcvNxt`
  左侧，不算在窗口内；因 `rcvWnd <= 2^30 < 2^31`，该比较天然排除
  左侧位置）。

## ACK 范围含两端的理由

区间 `[sndUna−maxSndWnd, sndNxt]` 取**闭合**：`Ack = sndNxt` 是对已
发出数据的合法确认（虽尚未真正发出也算可接受，符合 RFC 5961 的宽
松上界）；`Ack = sndUna−maxSndWnd` 是对端窗口最大时仍可能合法到达
的最旧重复 ACK。两端之外（更老或超前）即视为注入尝试，触发挑战
ACK 让对端用真实状态自证。

## 挑战 ACK 限速

限速器状态为窗口起点 `ws`（初为空）、窗口内挑战总数 `cnt` 与其中
RST 触发的 `cntR`（初均为 0），**只在需要发挑战 ACK 时才推进**：

- `ws` 为空或 `now−ws >= P` 时开新窗口：`ws=now, cnt=0, cntR=0`；
- `cnt < C` 且（非 RST 触发或 `cntR < ceil(C/2)`）时发出
  `AckChallenge` 并计数（RST 触发时 `cntR` 也加一），否则
  `Suppressed`，且被压制的挑战**不改变** `ws/cnt/cntR`；
- RST 触发的挑战至多占窗口配额的一半（向上取整），其余留给 SYN 与
  ACK 范围检查触发的挑战，避免 RST 洪泛耗尽配额。

任一窗口内挑战 ACK 数 `<= C`，其中 RST 触发数 `<= ceil(C/2)`；
`Drop` 与 `AckPlain` 完全不触碰限速器。

## 并发与确定性

`Process` 与 `Snapshot` 均可并发调用（内部互斥锁），结果等价于某个
串行顺序；相同段序列重放得到完全相同的动作、序号与限速计数
（`TestReplayDeterminism` 与 `TestConcurrency` 覆盖）。

## 本地验证

```bash
# 全部单测（规定示例 + 拒绝原因 + 限速/配额 + 并发）
go test ./tcpchk

# 竞态检测
go test -race ./tcpchk

# 2000 组随机段序列与朴素模拟对照，-v 打印每步输入/输出/判定依据
go test -run TestAgainstNaiveModel -v ./tcpchk
```

`model_test.go` 中的 `model` 是按规格逐条写成的朴素模拟，随机对照
测试逐步骤比较动作、错误原因、`rcvNxt/sndUna/maxSndWnd` 与
`ws/cnt/cntR`，并校验窗口配额不变量。
