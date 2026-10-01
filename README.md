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

## 按键消抖与手势识别

`gesture` 包从逐毫秒电平流中产生带采样序号的事件：`Press`、`Release`、`LongPress`、`SingleClick`、`DoubleClick`。

### 消抖计数

- 构造参数：`New(D, L, W)`，其中 `D` 为消抖次数，`L` 为长按门限，`W` 为双击间隔。
- 稳定电平初始为 `0`；原始电平与稳定电平相同，连续异电平计数清零。
- 原始电平与稳定电平不同且连续计数达到 `D` 时，稳定电平在当前采样序号翻转并清零计数。
- 稳定翻转为 `1` 产生 `Press`，翻转为 `0` 产生 `Release`。

### 短按与长按

- 按下序号记为 `tp`，释放序号记为 `tr`，按下时长为 `tr-tp`。
- 保持到序号 `tp+L`（含）时，在该序号产生一次且仅一次 `LongPress`。
- 若 `tp+L` 同时发生释放，事件顺序为 `LongPress`、`Release`。
- 按下时长不小于 `L` 的释放不是短按，释放后不产生单击。
- 按下时长小于 `L` 的释放是一次短按。

### 单击与双击

- 第一次短按释放于 `tr` 后，若下一次稳定按下序号 `tp2` 满足 `tp2-tr <= W`（含），则该按下为第二击。
- 第二击也短按时，在其释放序号先产生 `Release`，再产生 `DoubleClick`，随后重新计击，不产生第三击。
- 第二击成长按时只产生 `LongPress`；第一次短按计入作废数，不产生单击。
- 直到序号 `tr+W+1` 仍没有第二击时，在该序号产生 `SingleClick`。
- 当 `tp2=tr+W+1` 时，同一序号顺序为 `SingleClick`、`Press`，该按下重新作为第一轮第一击。
- `W=0` 时第二击必须与释放同序号才可能成立，而释放序号不可能再次按下，因此不会产生双击。

同一序号的总顺序为：到点事件（`LongPress`、`SingleClick`）优先，其次是稳定变化事件（`Press`、`Release`），最后是由释放产生的 `DoubleClick`。

### 参数与并发

- `D < 1` 返回 `ErrInvalidDebounce`，`L < 1` 返回 `ErrInvalidLongPressThreshold`，`W < 0` 返回 `ErrInvalidDoubleClickWindow`。
- `Sample` 或 `Run` 遇到非 `0/1` 电平返回 `ErrInvalidLevel`；被拒绝操作不改变状态，也不占用采样序号。
- `Run` 会先校验整批电平，再按顺序等价执行逐个 `Sample`，返回本次新增事件。
- 识别器内部使用互斥保护，并发调用等价于某个合法串行顺序；同一输入流任意切分到多次 `Run` 都得到相同事件表。

### 手势测试

测试覆盖消抖清零、计数 `D-1/D`、按下时长 `L-1/L`、窗口 `W/W+1`、`W=0`、第二击长按作废、同序号事件顺序、双击后重新计击、长按不单击、非法操作原子性、切分等价、并发串行化，以及 80 组随机电平与逐步朴素模拟逐条对照。

```bash
# 详细查看输入、输出和逐步判定依据
GOCACHE=/tmp/ontology-go-cache go test -v ./gesture

# 竞态检测
GOCACHE=/tmp/ontology-go-cache go test -race ./...
```

如果默认 Go 构建缓存目录不可写，可像上面一样将 `GOCACHE` 指到可写目录。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
