# creditflow — 基于信用的点对点流控

在**一个发送端**与**一个接收端**之间，按信用（credit）控制发送速率：
接收端按接收缓冲空位通告信用，发送端只在持有信用时发送，且**先扣信用再发送**；
暂时发不出的消息进入**积压（backlog）**，待新信用到达后自动补发。

## 状态与不变量

通道内部维护以下计数（全部由一把互斥锁保护，收发两侧可被不同执行体并发调用）：

| 量 | 含义 |
| --- | --- |
| `credit` | 发送端当前可用信用，任意时刻非负 |
| `backlog` | 已产生但尚未发送的积压消息数，`0..maxBacklog` |
| `inflight` | 已发送但接收端尚未取走的在途消息数 |
| `buffered` | 接收端已取走但尚未消费的缓冲占用数，`0..capacity` |
| `produced` | 已产生消息总数 |
| `received` | 已收到消息总数 |
| `consumed` | 已顺序消费消息总数 |

任意时刻维持：

- 信用非负；信用为零时**一条不发**。
- `buffered + inflight + credit <= capacity`（缓冲占用加在途信用不超过容量）。
- `produced == backlog + inflight + received`（已产生总数等于积压加已收到及在途）。
- 消费编号从 1 起**连续、无重复、无跳号**。

## 操作规则

### 发送端 `Sender`

- `Produce(payload) (seq, err)`：消息分配连续编号后**先进入积压**，随后自动发送——
  循环执行“信用 > 0 且积压非空 ⇒ 信用减 1、队首消息移入在途”，直到信用为零或积压清空。
- `Credit()` / `Backlog()`：只读查询当前可用信用与积压条数。
- `Probe() (granted, err)`：信用探测。**仅当 `credit == 0` 且积压非空时允许**；
  效果与接收端一次 `Advertise()` 完全相同。探测**不保底**（可能授予 0）、**不记忆**
  （这次没额度不会让下次通告变多）。

### 接收端 `Receiver`

- `Deliver() []Message`：把当前所有在途消息按编号顺序取入接收缓冲，**不触发通告**。
- `Consume(seq) error`：只能消费“下一期望编号”且确已到达的消息，**不触发通告**。
- `Advertise() (granted)`：计算

  ```
  free = capacity - buffered - credit - inflight
       = 缓冲余量(buffered 之外的空位) - 在途信用(credit + inflight)
  ```

  `free > 0` 时授予 `free` 个信用（发送端随后自动补发积压）；`free <= 0` 时**什么都不做**。
- `Buffer()` / `InFlight()`：只读查询缓冲占用与在途条数。

> 说明：`credit + inflight` 即“在途信用”——已经授予发送端、但尚未对应到接收缓冲空位的额度，
> 因此通告额度必须扣除它，保证发送永不超出接收缓冲。

## 边界与错误类别

三类错误互不相同，可用 `errors.Is` 区分；**任何一次被拒都整体失败、不改变任何状态（失败不留痕）**：

| 错误 | 触发条件 |
| --- | --- |
| `ErrInvalidParam` | 构造参数非正（`capacity <= 0` 或 `maxBacklog <= 0`）；消费编号非正、重复/过期、跳号或尚未到达 |
| `ErrProbeNotAllowed` | 探测时仍有信用，或积压为空 |
| `ErrBacklogOverflow` | `Produce` 会使积压超过 `maxBacklog` |

非法判定全部在修改状态之前完成，因此拒绝调用前后 `Stats()` 快照完全一致。

## 日志

每一步（含被拒绝的调用）都通过 `Logger` 打印一行，包含**输入参数、信用、积压、在途、缓冲与判定依据**；
被拒调用额外打印错误原因与“失败不留痕”。传 `nil` 使用输出到 stderr 的默认日志器。

## 本地验证

```bash
# 全量测试（详细输出）
go test -race -v ./creditflow

# 重复运行以压测并发路径
go test -race -count=20 ./creditflow

# 全工程 + 覆盖率
go test -race ./...
go test -coverprofile=/tmp/cov.out ./creditflow && go tool cover -func=/tmp/cov.out

# 格式与静态检查
gofmt -l .
go vet ./...
```

测试覆盖：信用与积压的增减及自动发送、通告为零/为正、投递不触发通告、
探测允许/拒绝/不保底/不记忆、三类非法输入及拒绝后状态不变、与独立“逐条发送”
参照模型逐步一致性比对，以及多执行体并发下的竞态检测与不变量抽查。
