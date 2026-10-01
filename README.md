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

## 嵌套向量中断控制器模型（`nvic` 包）

`nvic/nvic.go` 实现了一个可复现事件序列的 NVIC 模型；所有操作与查询
内部由互斥锁保护，可并发调用，结果等价于某个串行顺序。

### 优先级分组

- 构造 `New(M, s)`：`M >= 1` 个中断，`s ∈ [0,7]` 为子优先级位数。
- 每中断有使能 / 挂起 / 活动三个标志（初始均为假）与 8 位优先级 `p`
  （初始 0，数值小者更紧急）。
- 组优先级 `g(p) = p >> s`；子优先级为 `p` 的低 `s` 位。
- 屏蔽阈值 `b`（初始 0）：`b=0` 不屏蔽；`b>0` 时组优先级
  `g(p) >= g(b)=b>>s` 的中断被屏蔽（恰等于也屏蔽）。

### 候选与抢占判定

- 「候选」= 使能、挂起、非活动且未被屏蔽的中断中，按
  （组优先级，子优先级，编号）字典序最小者。
- 当前运行级 = 栈顶中断此刻的当前组优先级；栈空（线程态）为无穷大。
- `Enable/Disable/Pend/Clear/SetPriority/SetBase` 施加后，若候选存在且其
  组优先级**严格小于**当前运行级，则进入：清挂起、置活动、压栈，
  栈空产生 `Enter(irq)`、栈非空产生 `Preempt(irq)`。
- 组优先级相同而仅子优先级更小不构成抢占（只影响候选先后）。
- 判定一律使用各中断此刻的当前优先级与阈值（含运行级随 `SetPriority`
  即时变化）。

### 退出、尾链与恢复

`Return()` 在栈空时被拒绝（`ErrReturnFromThread`）；否则：

1. 栈顶中断退出：活动清除、出栈，产生 `Exit(irq)`。
2. 重新选候选：候选存在且其组优先级严格小于**新栈顶**运行级
   （栈空则任何候选都可）时，不经线程态直接进入，产生
   `TailChain(irq)`（可尾链进入刚退出的中断自己）。
3. 否则栈非空产生 `Resume(新栈顶)`，栈空产生 `Idle`。

活动中的中断可再被 `Pend`；它退出后若成为候选会立即被尾链进入。
`Disable` / `Clear` 只影响使能与挂起，不改变活动标志和运行栈。

### 拒绝原因（哨兵错误，可区分）

| 错误 | 触发条件 |
| --- | --- |
| `ErrInvalidInterruptCount` | `New` 时 `M < 1` |
| `ErrInvalidSubPriority` | `New` 时 `s ∉ [0,7]` |
| `ErrIRQOutOfRange` | 中断编号越界（优先于优先级非法判定） |
| `ErrInvalidPriority` | 优先级 ∉ `[0,255]` |
| `ErrInvalidBase` | 阈值 ∉ `[0,255]` |
| `ErrReturnFromThread` | 线程态（栈空）调用 `Return` |

被拒绝的操作不改变任何标志、优先级、阈值与运行栈。

### 本地验证

```bash
# 全量测试（场景 + 400 组随机序列与逐步朴素模拟对照）
go test -v ./nvic

# 竞态检测（含 8 goroutine 并发操作不变量检查）
go test -race -count=2 ./nvic

# 仅看逐步输入 / 输出事件 / 判定依据日志
go test -v -run TestScenario ./nvic

go vet ./nvic
gofmt -l nvic
```

`-v` 日志逐步打印每个操作的输入、输出事件及判定依据（如
"candidate group strictly smaller than running level"）。
`nvic/naive_test.go` 内的 `naiveModel` 是与生产实现完全独立的逐步朴素
模拟，随机测试逐拍对照事件序列、全部标志、优先级、阈值与运行栈，并
校验不变量：活动标志为真的中断恰好是栈内中断且栈内无重复；每个操作
完成后不存在组优先级严格小于当前运行级的候选；相同操作序列重放结果
完全相同。
