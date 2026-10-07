# 设计说明：按权限抢占的乐观并发更新（ontology 包）

## 1. 目标语义

多个不同权限等级（`Privilege`，数值越大权限越高）的动作，可在各自的
乐观重试循环中并发更新同一对象实例。必须同时满足：

1. **严格更高权限才能抢占**：权限相同的竞争者之间永远只是普通版本冲突，
   按到达顺序（或其它确定性规则）裁定，任何同权限情形都不得以权限抢占。
2. **抢占只在更高写入真实生效之后**：抢占不是“权限免疫”。高权限动作
   自身也会与同权限/更高权限动作发生普通冲突并重试；只有当它的写入
   真正提交、推进了版本之后，仍在重试循环中的低权限动作才被终止。
3. **立即终止且不消耗预算**：被抢占动作返回与“普通冲突重试”“预算耗尽”
   “成功提交”都不同、可单独识别的 `StatusPreempted`，且终止发生在
   判定的当次尝试，不需要把重试预算用尽。
4. **判定顺序**：`committed` 与 `preempted` 的判定先于“预算耗尽”。
   即使本次冲突尝试恰好用完最后一次预算，只要抢占条件成立，
   结果仍是 `preempted` 而不是 `exhausted`。
5. **终止无副作用**：抢占终止是纯查询结果，不推进版本号、不改属性、
   不动任何时钟状态；实例的唯一变化来自真正生效的写入。
6. **串行等价**：任意并发交错的最终状态，等价于把所有生效写入按其
   提交版本号排成的某个串行序列逐个执行；且在该序列中，抢占了某个
   低权限动作的高权限写入，其生效位置不晚于（实际就是先于）该低权限
   动作本应生效的位置——被抢占动作根本不出现在生效序列里。

四类互斥结果：`StatusCommitted` / `StatusPreempted` /
`StatusExhausted`，加上“尚未终结的在途尝试”这一内部状态。

## 2. 核心数据结构

### 2.1 实例状态（`instance.go`）

每个实例维护：

```
version     int64      // 单调版本号，每次生效写入 +1
attrs       Attrs      // 当前属性
highWater   Privilege  // 自版本 0 以来生效过的最高写权限（不对外暴露）
baseWater[v] Privilege // 产生版本 v 的那次写入“生效之前”的高水位
```

`baseWater` 是关键的取舍点。仅用一个全局 `highWater` 无法区分两种情况：

- 版本 v 由一个**普通权限**写入产生，但历史上更早有过更高权限写入；
- 版本 v 本身就是被更高权限写入推进到的。

读基线必须携带 `baseWater[version]`（即“读到的版本是在什么高水位下
产生的”），这样抢占判定才能精确表达“**自这条基线以来**是否出现过
严格更高权限的生效写入”。`baseWater` 的长度只随版本数（生效写入数）
增长，与当前并发竞争者数量无关。

### 2.2 O(1) 抢占判定（`Instance.tryCommit`）

在实例锁内，严格按以下顺序判定单次尝试：

```
if highWater > base.HighWater && highWater > priv {
    return preempted            // 抢占先于一切，也先于成功提交
}
if version == base.Version {
    // 记录 baseWater[newVersion] = oldHighWater，再推进版本/属性/高水位
    return committed
}
return conflict                 // 普通版本冲突，调用方带着新基线重试
```

为什么 `highWater > base.HighWater && highWater > priv` 与“自基线以来
存在严格更高权限生效写入”等价：

- `highWater > base.HighWater`：高水位在该基线之后被抬升过；
- `highWater > priv`：抬升它的某次写入权限严格高于本动作。

两者合取恰好刻画了“一个严格更高权限的写入，在本动作读取基线之后
生效”。该判定只读取两个标量并比较，**O(1)，不随并发竞争同一实例的
动作总数 N 增长**（无等待者列表、无广播）。

抢占检查放在版本匹配之前，是为了覆盖“动作读到的版本恰好没有其它
新写入，但产生该版本的写入本身就是更高权限”的情况——此时版本号
相等却仍必须终止，而不是错误地提交成功。

### 2.3 重试循环（`executor.go`）

每次尝试：等待读闸门 → 锁内读一致快照（版本、高水位、属性）→
释放锁并停在提交闸门 → 被放行后重新取锁执行 `tryCommit` →
登记完整 `AttemptRecord` → 解锁。

终结判定顺序保证：

```
switch verdict {
case committed:   -> StatusCommitted
case preempted:   -> StatusPreempted   // 立即返回，不看预算
case conflict:
    if attempt == MaxAttempts { -> StatusExhausted }
    // 否则基于最新快照进入下一次尝试
}
```

因此“最后一次预算上的冲突若同时满足抢占条件”会落入 `preempted`，
抢占不消耗预算这一点由判定顺序在代码层面直接保证。

每次尝试都记录 `AttemptRecord{权限, 基线版本, 基线高水位, 判定时版本,
判定时高水位, verdict, 提交版本}`，完整可重放。

## 3. 确定性并发与测试可重放性（`scheduler.go`）

真实并发用无钩子执行器（`NewExecutor(nil)` + goroutine）即可。
为了可验证地构造任意交织，实现了一个脚本化调度器，提供两道闸门：

- `BeforeRead`：一次尝试读取基线之前在此注册停放；
- `BeforeCommit`：读完快照、执行 CAS 之前在此停放（携带读到的基线）；
- `AfterCommit`：CAS 落定后通知驱动循环。

驱动脚本 `[]Step{{OpID, Commit}}` 精确决定：

1. 放行哪些尝试去读（连续放行多个读，它们之间没有 CAS，于是读到
   **同一基线**——这构造了“多个动作同时在途”的场景）；
2. 以什么顺序放行它们的 CAS（决定谁先生效、谁看到过期基线）。

脚本执行过程中调度器把每个 `BeforeCommit`（读事件）与每个
`AfterCommit`（判定事件）追加进**事件流** `[]SchedEvent`。事件流就是
重放与对照所需的全部输入，不依赖额外对外暴露的状态。

无钩子模式下动作真并发运行（`TestRealConcurrencyRace` 在 `-race` 下
反复验证）；脚本模式下逐尝试确定性（同权限多方、交替抢占、高权限
自身冲突重试等场景测试重复运行结果稳定）。

## 4. 独立参照模型（`reference.go`）

参照模型**不复用** `Instance`/`Executor` 的任何实现代码，它只消费
动作定义与事件流，用一个朴素的串行状态机重放：

- 维护自己的 `version / highWater / baseWater / attrs`；
- 在读事件处登记该尝试的基线；
- 在判定事件处独立计算 `committed / preempted / conflict`，并对
  committed 独立地重新施加一次 `op.Apply`、推进自己的状态。

抢占判定同时给出两种表述并交叉核对：

- **高水位表述**（与实现同形，但数值全部由参照模型自行重放得到）：
  `st.highWater > base.HighWater && st.highWater > op.Priv`；
- **朴素历史扫描**（O(历史长度)，刻意不优化）：在已生效写入序列中
  找到一个权限严格更高、且产生或晚于本基线版本的写入。

差分测试（`TestRandomDifferential`，300 个随机种子，随机权限等级、
随机预算、随机读写/CAS 交织）要求实现与参照模型在**每次尝试的
verdict、每个动作的最终状态与终结版本、实例最终版本/高水位/属性**
上完全一致。

## 5. 关键取舍与被放弃的方案

- **放弃“每个等待者一个 abort 通道 / 写后广播所有在途动作”**：那会让
  抢占代价随竞争者数 N 线性增长。改用单调高水位 + 基线高水位，判定
  降为 O(1)，且不需要额外对外暴露状态。
- **放弃只用一个全局高水位、不带 `baseWater`**：无法区分“历史上的
  高权限写入”与“恰好产生当前基线的写入”，会把普通权限写入误判为
  抢占，差分测试立刻能抓到。
- **放弃“权限高就免疫冲突”**：高权限动作在同权限/更高权限竞争下仍
  走普通 CAS 冲突重试（见 `TestHighPrivilegeActionRetriesToo`）。
  抢占严格以“更高写入已生效”为前提。
- **放弃“抢占也计入一次重试失败”**：判定顺序把 preempted 放在预算
  判断之前，最后一次预算上也立即终止（`TestPreemptionBeforeBudgetExhaustion`）。
- **放弃在抢占路径上写任何状态**：preempted 分支在改版本/属性之前
  返回，`TestAlternatingPreemption` 断言最终版本号恰好等于真实生效
  写入次数，属性只含这些写入的叠加。
- **确定性测试用“双闸门 + 事件流”而不是 sleep/时序猜测**：脚本可
  精确复现“多个动作读到同一旧基线、CAS 按指定顺序生效”，无需依赖
  goroutine 调度；同一份事件流直接喂给独立参照模型。

## 6. 本地验证方法

环境：Go 1.26（仓库 `go.mod` 为 `go 1.26.5`）。若 `go` 不在 PATH，
使用 `/usr/local/go/bin`。

```bash
# 常规全量测试
go test ./...

# 竞态检测 + 详细输出
go test -race -v ./...

# 只跑抢占相关场景 / 随机差分 / 真实并发
go test -v ./ontology -run 'TestSamePrivilegeNoPreemption|TestAlternatingPreemption|TestPreemptionBeforeBudgetExhaustion|TestHighPrivilegeActionRetriesToo|TestHighWaterMonotonic'
go test -v ./ontology -run TestRandomDifferential
go test -race ./ontology -run TestRealConcurrencyRace

# O(1) 开销不随竞争者数增长的证据（ns/op 在 n=1..4096 下基本持平）
go test -bench=Preemption -benchmem ./ontology

go vet ./...
gofmt -l .
```

每个动作的权限等级、判定基线（版本+高水位）、每次尝试 verdict、
最终结果与提交版本都记录在 `AttemptRecord`/`SchedEvent` 中，
可直接打印用于重放核验。
