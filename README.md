# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 按键消抖与手势识别

`gesture.GestureRecognizer` 按毫秒接收原始电平，构造参数为：

- `D`：消抖次数，至少为 1；原始电平连续 `D` 次不同于稳定电平时，稳定电平在第 `D` 个采样序号翻转，计数同时清零。
- `L`：长按门限，至少为 1；稳定按下序号为 `tp` 时，在 `tp+L` 产生一次 `LongPress`。释放恰好发生在该序号时，顺序也是 `LongPress` 先于 `Release`。
- `W`：第二击允许间隔，不能为负；短按释放序号为 `tr` 时，`tp2-tr <= W` 的下一次稳定按下是第二击。

原始电平与稳定电平相同会立即把消抖连续计数清零，因此抖动反复不会累计。稳定按下后的按下时长为 `tr-tp`：

- `tr-tp >= L` 是长按；长按释放后不再产生单击。
- `tr-tp < L` 是短按。第一次短按后等待第二击；第二击也短按时，在其 `Release` 后产生 `DoubleClick`，然后重新从第一击计数，不存在三击。
- 第二击在释放前达到长按门限时只产生 `LongPress`，第一次短按作废，不产生单击。
- 若到 `tr+W+1` 仍没有第二击，则在该序号产生 `SingleClick`；若该序号恰好稳定按下，顺序为 `SingleClick`、`Press`，本次按下属于新一轮第一击。

同一序号上的事件顺序固定为：

1. 到点事件：`LongPress`、`SingleClick`。
2. 本采样引起的稳定变化：`Press`、`Release`。
3. 释放派生出的 `DoubleClick`。

`Sample(level)` 返回本次新增事件；`Run(levels)` 先校验整批电平，任一值不是 0 或 1 时整批拒绝且不改变状态，然后等价于按序调用 `Sample`。所有输入、事件查询和统计查询均由同一把互斥锁串行化。`Events()` 可重放完整事件表，`Stats()` 可核对恒等式：短按总数 = 单击数 + 2 × 双击数 + 作废数 + 待定数。

非法构造与输入返回可区分的哨兵错误：`ErrInvalidDebounceCount`、`ErrInvalidLongPressThreshold`、`ErrInvalidDoubleClickWindow`、`ErrInvalidLevel`。

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

# 手势识别器：打印输入、输出与逐步判定依据
go test -v ./gesture

# 手势识别器：并发安全验证
go test -race ./gesture

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
