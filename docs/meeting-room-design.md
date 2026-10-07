# 在线会议室发言权控制服务 — 设计说明

## 模块划分

| 模块 | 文件 | 职责 |
| --- | --- | --- |
| 类型与快照 | `meeting/types.go` | 角色、快照、参数合法域等公开类型 |
| 拒绝模型 | `meeting/errors.go` | 七级拒绝类别与可区分原因码，定义“只报第一个”的优先级 |
| 举手队列 | `meeting/queue.go` | 以进入序号为键、随机优先级为堆序的 Treap，O(log n) 入队/移出/名次 |
| 会议室核心 | `meeting/room.go` | 状态定义、惰性到期引擎 `advance`、顺延 `autoGrant`、主持人移交 |
| 操作层 | `meeting/ops.go` | 12 个公开操作，统一按拒绝类别次序校验后委托核心状态机 |
| 朴素参考实现 | `meeting/naive/naive.go` | 切片队列 + 线性扫描的独立实现，仅用于差分对照 |

协作关系：操作层只做校验与编排；状态变化全部收敛到 `room.go` 的少数
原语（`advance` / `autoGrant` / `transferHost` / 队列操作），保证
“到期顺延”“静音顺延”“离开顺延”三条路径复用同一段代码，行为天然一致。

## 关键语义决策

1. **惰性到期**：不依赖任何定时器或墙钟。每个携带 `now` 的操作先经
   `prologue`：参数（时刻）非法或时钟回退 → 直接拒绝且不做任何处理；
   否则先 `advance(now)` 再推进时钟，之后才做关闭/权限/状态检查。
   因此被拒操作（只要过了参数与时钟检查）也会触发到期处理并推进时钟，
   与规格一致。`advance` 每轮以“上一轮的到期时刻”作为新的授予时刻，
   循环至发言者未到期或队列为空，一次操作可触发多轮顺延。
2. **恰等于到期时刻即到期**：判定条件为 `grantAt + S <= now`。
3. **顺延的授予时刻**：到期顺延取到期时刻；静音发言者、发言者离开
   取该操作的 `now`；主持人手动 `Grant` 取操作 `now`。
4. **Yield 不自动顺延**：规格仅对到期、静音、离开三种情形明确要求
   顺延，对 Yield 只要求“交还”。因此 Yield 后发言权空闲，待主持人
   或协管员再次 `Grant`。
5. **Appoint/Revoke 权限**：规格原文“主持人与协管员可 Appoint 协管员
   或撤销协管员，仅主持人可以”存在歧义，按“Appoint：主持人与协管员；
   Revoke：仅主持人”实现，并在本节显式记录。
6. **QueuePos 为纯查询**：不带 `now`，不做惰性处理、不推进时钟；
   房间关闭后报已关闭。
7. **拒绝次序**：每个操作严格按 参数非法 > 时钟回退 > 房间已关闭 >
   操作者不在室内 > 权限不足 > 目标不存在 > 状态不允许 的顺序短路，
   只报告第一个失败类别；状态类内部再以可区分原因码细分
   （重复举手 / 已在发言 / 被静音 / 队列满 等）。

## 并发与确定性

- 所有导出方法在同一把 `sync.Mutex` 下执行，操作天然可线性化，
  并发调用等价于某个串行顺序；`-race` 下的并发测试验证无数据竞争，
  并在并发结束后校验不变量（发言者至多一人、不在队列中、队列无重复、
  不超容量、恰一名主持人）。
- 无任何墙钟、全局随机源或 map 迭代顺序泄漏到结果：Treap 优先级来自
  以房间配置为种子的 splitmix64；快照成员按加入序号排序；主持人移交
  按加入序号取最小。相同操作序列重放得到完全相同的结果
  （`TestReplayDeterminism`、`TestHandQueueDeterministicShape`）。

## 关键取舍

- **Treap 而非链表**：链表移出 O(1) 但名次查询 O(n)，违反
  “QueuePos 不得随队列长度线性增长”。Treap 以期望 O(log n) 同时满足
  入队、任意移出、名次查询，且实现远短于红黑树/AVL。
- **进入序号作键**：入队即最大键，天然支持尾部 merge；成员→序号的
  哈希表让任意移出无需查找位置。
- **惰性到期而非主动定时**：结果只取决于操作序列，可精确复现；
  开销只与实际到期轮数成正比。
- **单互斥锁而非细粒度锁/无锁结构**：状态机耦合度高（到期、顺延、
  移交相互影响），细粒度锁会引入难以推理的中间态；单锁简单且
  可证明地满足“等价于某个串行顺序”。

## 被放弃的方案

- **切片/链表队列 + 线性名次查询**：违反 QueuePos 复杂度要求，放弃。
- **小根堆（按到期时间）+ 定时器主动回收**：引入墙钟依赖，重放不可
  复现，且“操作前处理全部到期”语义仍需惰性路径，放弃。
- **Yield 后自动顺延**：规格未要求，且与“交还后由主持人决定”的
  常见会议语义冲突，放弃（见关键语义决策 4）。
- **读写锁分离**：快照需先执行惰性到期（写操作），读锁无法承载，
  放弃。

## 性能与可验证证明

- `meeting/queue.go` 内置 `visits` 结点访问计数器，
  `TestHandQueueComplexityBound` 断言：满容量 500 时树高 ≤ 4·log2(n+1)，
  且 `pos`/`remove`/`popFront` 的实际访问结点数分别以树高的
  1/3/2 倍为上界 —— 用计数而非计时证明非线性增长。
- `meeting/room.go` 内置 `expiryRounds` 计数器，`TestExpiryRoundsProbe`
  断言：500 名成员下无到期时处理轮数为 0，级联场景轮数恰为 k ——
  证明惰性处理开销只与实际到期轮数有关。
- `meeting/bench_test.go` 提供基准：`QueuePos`/`Raise+Lower`/`remove`
  在 n=100 与 n=500 下耗时基本持平；`advance` 在 10 与 500 名成员下
  均为常数；级联场景耗时随轮数线性增长。

## 测试覆盖

- 到期恰好相等与差一秒：`TestExpiryExactlyAtDeadline`
- 一次操作多轮顺延、授予时刻取到期时刻：`TestCascadeAndGrantTimeAtExpiry`
- 静音发言者 / 静音队列成员：`TestMuteSpeaker`、`TestMuteQueuedMember`
- 主持人移交三优先级：`TestHostTransferPriorities`
- 关闭后拒绝：`TestHostTransferPriorities`（第三优先级段）
- 拒绝次序六对相邻类别：`TestRejectOrderAdjacentPairs`
- 时钟推进规则（被拒仍推进 / 参数非法与回退不推进）：
  `TestClockAdvanceOnRejectedOps`
- 并发可串行化与唯一发言者：`TestConcurrentOpsSerializable`、
  `TestConcurrentGrantSingleSpeaker`
- 重放确定性：`TestReplayDeterminism`
- 与独立朴素模型对照 1500 组随机序列（打印输入、输出、判定依据）：
  `TestDifferentialAgainstNaive`

## 本地验证方法

```bash
export PATH=$PATH:/usr/local/go/bin

# 全量测试（含竞态检测）
go test -race ./...

# 差分对照的完整日志（输入/输出/判定依据）
go test ./meeting/ -run TestDifferentialAgainstNaive -v

# 性能基准
go test ./meeting/ -run XXX -bench . -benchtime 1000x

# 静态检查
go vet ./... && gofmt -l .
```
