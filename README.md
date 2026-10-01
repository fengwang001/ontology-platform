# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 按键消抖与手势识别

`gesture` 包提供 `gesture.New(D, L, W)` 与 `(*Recognizer).Sample(level)`、`(*Recognizer).Run(levels)`。`Sample` 每次输入一个逐毫秒电平，采样序号从 0 开始；返回本次新产生的事件，事件类型包括 `Press`、`Release`、`LongPress`、`SingleClick`、`DoubleClick`，每个事件都带产生序号。

### 消抖计数

稳定电平初始为 0。原始电平与稳定电平不同且连续出现时，消抖计数加 1；再次读到稳定电平时计数清零。计数达到 `D` 的采样点立即翻转稳定电平并清零计数，该采样序号就是稳定 `Press` 或 `Release` 的序号。`D` 必须不小于 1。

### 短按与长按

稳定按下序号记为 `tp`，稳定释放序号记为 `tr`，按下时长为 `tr-tp`。

- 按下保持到 `tp+L`（含相等）时，在 `tp+L` 产生且只产生一次 `LongPress`。
- 释放也发生在 `tp+L` 时，同一序号先产生 `LongPress`，再产生 `Release`。
- 按下时长不小于 `L` 的释放不会再产生单击。
- 按下时长小于 `L` 的释放记为一次短按。

### 单击与双击裁决

第一次短按后记录释放序号 `tr`：

- 若下一次稳定按下序号 `tp2` 满足 `tp2-tr <= W`，该按下作为第二击。
- 第二击仍以短按结束时，在其释放事件之后产生 `DoubleClick`，随后重新从第一击开始计数，不存在三击。
- 第二击达到长按时，只产生该次 `LongPress`，第一次短按作废且不产生单击。
- 若到 `tr+W+1` 仍没有第二击，则在该序号产生 `SingleClick`。
- `W=0` 时，第二击最早也要在 `tr+1` 发生，因此永远不会形成双击。
- 若 `tr+W+1` 当点恰好发生稳定按下，则顺序为 `SingleClick`、`Press`，新按下作为新一轮第一击。

同一序号的总顺序是：到点事件（`LongPress`、`SingleClick`）先于该采样造成的稳定变化（`Press`、`Release`）；若释放产生双击，则顺序为 `Release`、`DoubleClick`。

### 参数与错误

- `D < 1` 返回 `ErrInvalidDebounce`。
- `L < 1` 返回 `ErrInvalidLongPress`。
- `W < 0` 返回 `ErrInvalidDoubleClickWindow`。
- `Sample` 的电平不是 0 或 1 时返回 `ErrInvalidLevel`，不改变状态和序号。
- `Run` 先检查整批电平，任一项非法就整体返回 `ErrInvalidLevel`，不会送入任何采样；合法时等价于按顺序逐个调用 `Sample`。

识别器内部使用互斥锁，`Sample` 与 `Run` 可并发调用，结果等价于某个合法的串行调用顺序。

### 本地验证

```bash
go test ./...
go test -race ./gesture
go test -v ./gesture
go vet ./...
gofmt -l .
```

`gesture/gesture_test.go` 中包含一个独立的逐步朴素模拟器；测试日志会打印输入电平、实际输出、朴素模拟输出以及每个判定依据。覆盖点包括抖动清零、`D` 与 `D-1`、时长 `L` 与 `L-1`、间隔 `W` 与 `W+1`、`W=0`、第二击长按作废、同序号事件顺序、双击后第三次按下重置、长按释放后无单击、随机输入对照、`Run` 任意切分等价性、重放等价性和并发调用。

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
