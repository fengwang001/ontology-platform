# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 可在线升级窗口大小的滚动窗口聚合器

实现位于 `windowing` 包（`windowing/aggregator.go`）：事件在流不停的前提下，
窗口大小可从旧值在线切换到新值；每个事件恰好落入且仅落入一套窗口划分，
已经发出的结果永不改写。

### 事件、窗口与输出

- 事件：`{Key, EventTime int64, Value int64}`，事件时间不允许为负（负值整体拒绝）。
- 窗口以时间 0 为原点、左闭右开：大小为 `S` 时，事件时间 `t` 属于
  `[⌊t/S⌋·S, ⌊t/S⌋·S + S)`。
- 每个窗口对每个键输出一条 `Result{Key, Start, End, Count, Sum}`。
- 输出条件为 `Watermark >= End`，输出顺序固定为（窗口右端 `End` 升序、
  同右端再按 `Key` 升序），输出后窗口状态立即清除。

### 水位、迟到判定与归属

- 水位初值 `0`，固定延迟 `AllowedLateness`（`D`），只增不减：
  `WM = max(WM, maxEventTime - D)`。
- 每条事件**先**按处理前水位判定迟到，再更新水位并输出到期窗口：
  - 所属窗口右端 `End <= WM`（处理前水位）⇒ 迟到，丢弃并计入 `LateCount`，
    不参与聚合、不影响 `maxEventTime`；
  - 否则计入对应窗口，随后更新水位并输出所有到期窗口。
- 升级待完成期间按事件时间选择划分：`t < B` 用旧大小，`t >= B` 用新大小；
  迟到判定也使用该事件**实际归属**的那套划分。

### 生效点 B 的推导

申请把大小从当前值 `S0` 改为 `S` 时：

1. `L = lcm(S0, S)`。要求 `L <= 10^9`（只关心正整数大小）。
2. `M = max(当前水位, 所有仍持有状态的窗口右端)`。
3. 生效点

   `B = ⌈M / L⌉ · L`

   即 `L` 的整数倍中不小于 `M` 的最小者。

几何含义：`B` 同时是 `S0` 与 `S` 的整数倍，所以它既是旧划分的窗口边界，
也是新划分的窗口边界；以 `B` 为切分点，旧侧窗口 `[..., B)` 与新侧窗口
`[B, ...)` 首尾相接、互不重叠，不会有任何事件被两套划分重复计数或遗漏。

典型例子：`4 -> 6` 时 `lcm=12`。

- 申请时 `M=12`（例如水位 11 且持有右端 12 的窗口）⇒ `B=12`。
- 申请时 `M=16`（例如水位 12 且持有右端 16 的窗口）⇒ `B=24`。

### 升级完成条件

- `WM >= B` 时升级完成，当前大小改为 `S`。
- 完成可能发生在后续事件推进水位之后；若申请瞬间已有 `WM >= B`（此时边界前
  窗口必已全部输出），则申请当场完成。
- 完成后 `B` 两侧的窗口按右端统一排序输出，因此旧侧窗口必然先于新侧窗口发出。

### 拒绝规则（按此顺序，只报第一个原因）

`RequestResize(S)` 在下列情况下整体拒绝，返回带 `ResizeRejectReason` 的错误，
且**不改变**水位、窗口状态与迟到计数：

1. `ResizeNonPositive`：`S <= 0`；
2. `ResizePending`：已有升级待完成；
3. `ResizeSameSize`：`S` 与当前大小相同；
4. `ResizeLCMTooLarge`：`lcm(S0, S) > 10^9`（中间乘法做 int64 溢出检测）。

### 并发与确定性

- 所有提交与升级申请由同一把互斥锁串行化，可被任意 goroutine 并发调用；
  每条事件只按某个串行顺序下的一套划分归属。
- 恒等不变量：`总提交数 = 已输出条数 + 迟到数 + 在途窗口内条数`
  （`AcceptedCount = EmittedCount + InFlightCount`，`LateCount` 单列）。
- 操作对状态的作用只取决于操作序列本身，因此同一序列重放得到字节级相同的
  输出（顺序、区间、条数、求和均一致）。
- 通过 `Config.Logger` 可记录每一步的输入、输出与判定依据
  （使用哪套划分、窗口区间、判定时水位、`end<=wm` 的迟到理由、`lcm/M/B` 推导）。

### 用法示例

```go
var out []windowing.Result
a := windowing.New(windowing.Config{
    InitialSize:     4,
    AllowedLateness: 2,
    Emit:            func(r windowing.Result) { out = append(out, r) },
})

a.Submit(windowing.Event{Key: "a", EventTime: 11, Value: 1})
b, err := a.RequestResize(6) // lcm=12，按当时 M 得出生效点 B
a.Submit(windowing.Event{Key: "a", EventTime: 13, Value: 2}) // t>=B 时按大小 6
```

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
go test ./windowing
go test -run TestResizeBoundary4to6 ./windowing -v

# 反复压并发提交/升级
go test -race -run TestConcurrentSubmitAndResize -count=20 ./windowing

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

`windowing` 测试覆盖：

- `4 -> 6` 时 `M=12 ⇒ B=12` 与 `M=16 ⇒ B=24` 两个生效点；
- 升级期间事件时间在 `B` 两侧分别归属旧/新划分；
- 升级完成后旧窗口先于新窗口输出，同右端按键升序；
- 迟到判定分别按两套划分（升级前、升级待完成期、升级完成后）；
- 负事件时间、非正大小、重复升级、相同大小、lcm 超限（含 int64 溢出安全）；
- 拒绝操作前后水位/窗口/迟到计数快照不变；
- 并发提交 + 并发升级的恒等不变量（`-race`）与同序列重放确定性；
- `-v` 日志中包含每条输入、每条输出及判定依据。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
