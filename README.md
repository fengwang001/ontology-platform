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

## 抢占阈值调度器（`scheduler` 包）

单核离散步进的抢占阈值调度（Preemption Threshold Scheduling）模型，构造参数为
加成档宽 `W`（1..10^6）、加成上限 `Bmax`（0..1000）、保护次数 `M`（1..1000）与
在册任务上限 `Tmax`（1..10^6）；任一参数越界则整体以 `ErrInvalidConfig` 拒绝。
当前时刻 `now` 初值为 0。

### 有效优先级与屏蔽值

- 每个就绪任务的有效优先级 `e = p + min(Bmax, floor(wt/W))`，其中 `wt` 为等待计数；
  候选 σ 为 `e` 最大者，并列取到达序号 `seq` 小者。
- 运行者 ρ 的屏蔽值 `sh = th`（运行阈值）；当 ρ 的被抢占次数 `pc >= M` 时
  `sh = +Inf`（`InfShield`），即受保护、不再被抢占。
- 抢占判定为严格大于：仅当 `e(σ) > sh` 时 σ 抢占 ρ；没有运行者时的派发不看阈值。

### tick 内三步次序

`Step()` 推进一个 tick（第 k 个 tick 开始时 `now = k-1`），依次：

1. **决策**：只用本 tick 开始时的 `wt` 计算有效优先级并选出 σ；无运行者则派发 σ
   （`wt` 清零），`e(σ) > sh` 则 ρ 回到就绪（`pc` 加一、`wt` 清零）并派发 σ（`wt` 清零）。
2. **执行**：运行者剩余工作量减一，其余就绪任务 `wt` 各加一；运行者剩余为 0 则
   完成于时刻 `now+1` 并移出在册（编号可重新使用）。
3. **时钟**：`now` 加一。`Step` 返回本 tick 运行者编号（空转为 0）与完成者编号。

`Add(id, p, th, w)` 在当前 `now` 到达任务并领取从 1 起的到达序号 `seq`；按
「参数非法（`ErrInvalidParam`）→ 编号已存在（`ErrDuplicate`）→ 已满（`ErrFull`）」
的顺序只报第一个错误，被拒绝的 `Add` 不改变任何任务与 `seq` 计数。

### 等待加成与防饿死

- 就绪任务每等待一个 tick `wt` 加一，加成 `floor(wt/W)` 封顶 `Bmax`，保证长等待
  任务最终超过任意固定屏蔽值，防止饥饿。
- `wt` 在派发与被抢占时都清零；`pc >= M` 的任务一旦运行即受保护，不会再被抢占。
- 任意时刻至多一个运行者；所有任务已执行 tick 数之和等于其工作量与剩余之差之和；
  相同操作序列重放得到完全相同的运行序列与完成时刻。

### 决策的次线性实现

决策不扫描全部就绪任务：就绪任务按钳制加成 `min(Bmax, wt/W)` 分入 `Bmax+1` 个桶，
每桶以索引堆维护「优先级最大、`seq` 最小」的任务，决策只比较各非空桶的堆顶
（最多 `Bmax+1` 个候选，与就绪任务数无关）。非导出计数器 `examined` 记录决策
比较的候选数（`Examined()` 读取），`TestExaminedScalability` 验证就绪任务 100 与
10000 两档（全部任务同时越过加成档）下平均每次 `Step` 的 `examined` 之比小于 4
（实测为 1，线性扫描接近 100）。

### 并发与可复现

所有方法（`Add`/`Step`/`Now`/`Examined`/`LastDecision`）均可并发调用，内部互斥锁
保证结果等价于某个串行顺序；`LastDecision()` 返回最近一个 tick 的候选 `e`、屏蔽值
与判定依据（`idle-dispatch`/`preempt`/`shielded`/`solo`/`idle`）。

### 本地验证

```bash
go test ./scheduler/          # 单元测试 + 2000 组随机参数对照朴素模拟
go test -race ./scheduler/    # 竞态检测
go test -v -run TestSpecExample ./scheduler/  # 查看规格示例的逐 tick 决策日志
go test -v -run TestExaminedScalability ./scheduler/  # 查看 examined 扩展性数据
```
