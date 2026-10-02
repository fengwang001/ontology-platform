# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 支付卡不可能行程检测器

实现位于 `paymentcard/detector.go`，入口是 `paymentcard.NewDetector(V, K, H)`。`V` 为 1 到 1,000,000 公里/小时，`K` 为 1 到 100，`H` 为 1 到 10^12 秒。`Check(card, t, x, y)` 的 `t` 为 0 到 10^12 秒，`x`、`y` 为 -10^9 到 10^9 公里，卡号必须是非空字节串。

### 判定顺序

每张卡维护最近一次被接受交易的锚点 `(t0,x0,y0)`、不可能行程拒绝历史、冻结标志和差旅窗口。参数校验通过后，单笔交易严格按以下顺序返回第一个原因：

1. 卡已冻结：`frozen`。
2. `(t,x,y)` 与锚点三项完全相同：`duplicate`。
3. `t < t0`：`out_of_order`；`t == t0` 但坐标不同不是乱序。
4. 无锚点：接受。
5. 差旅窗口满足 `from ≤ t < to`：免检接受。
6. 不满足速度公式：`impossible_travel`；否则接受。

除不可能行程外，所有被拒绝操作都不改变状态。不可能行程只追加拒绝历史并可能冻结，不改变锚点；接受（含免检接受）会推进锚点，拒绝历史保留。

### 整数速度公式

速度判定使用整数，避免浮点误差：

```text
d  = |x-x0| + |y-y0|
dt = t-t0
d*3600 <= V*dt 时接受
```

恰等接受。`dt=0,d=0` 已先判为重复；所以公式处的 `dt=0` 且 `d>0` 必为不可能行程。题设参数范围内该乘积不会溢出 Go `int64`。

### 拒绝窗口

不可能行程时先把本笔时刻 `t` 加入历史，再计算：

```text
cnt = 1 + 历史中此前满足 t_j > t-H 的项数
```

`t_j == t-H` 已过期；`t_j > t` 的未来历史项也计入，历史不要求单调。`cnt >= K` 时冻结，但当前这笔仍返回 `impossible_travel`。`Unfreeze` 清空冻结标志和拒绝历史，锚点与差旅窗口保留。

### 差旅与批量

`Declare(card, from, to)` 要求 `0 ≤ from < to ≤ 10^13`，窗口为左闭右开，每卡只保留最后一次登记，也可为从未交易过的卡登记。`CheckBatch` 的大小为 1 到 1000；任一笔非法则整批返回等长的 `invalid_parameters` 且不改状态。合法批次先按 `t` 升序稳定排序，同刻保持原下标，再把排序后的交易逐笔套用单笔状态机。整个批次是一个原子步骤，中途冻结后其余交易返回 `frozen`；返回值按原下标排列。

`Check`、`CheckBatch`、`Declare`、`Unfreeze` 和 `Snapshot` 均可并发调用，内部互斥锁保证结果等价于某个串行顺序。`Snapshot` 返回历史副本。

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

# 2000 组随机交易/批次与朴素模拟对照；-v 日志包含输入、输出和判定依据
go test -run TestRandomNaiveComparison -v ./paymentcard

# 竞态检测
go test -race ./...

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
