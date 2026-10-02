# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 突发积分实例计量器

`burstmeter.Meter` 按分钟计量实例积分，所有积分均以厘积分为单位。构造参数包括核数 `n`、基线百分比 `b`、余额上限 `Cmax`、启动积分 `L`、欠额结算窗口 `W`、欠额单价 `price`、欠额总量上限 `Dmax` 和初始模式。

每分钟固定入账 `e=n*b`；利用率为 `u` 个百分点时，一分钟消耗 `x=n*u`。

### Tick 四步次序

`Tick(m, u)` 必须严格按以下顺序执行：

1. **到期计费**：遍历欠额批次，凡 `m-产生分钟 >= W`，按“剩余欠额 * price”计入本分钟费用并移除；相等时立即到期。
2. **入账偿还**：先把 `e` 按 FIFO 偿还最旧批次，全部批次偿清后的剩余入账才加入 `bal`。
3. **消耗**：先用只减不增的启动积分 `lb`，再用 `bal`。仍不足时，无限模式追加欠额批次，标准模式计入被限速量。
4. **封顶丢弃**：消耗完成后，若 `bal > Cmax`，超出部分计入本分钟和累计丢弃量，并将 `bal` 置为 `Cmax`。

因此，同分钟入账即使让余额瞬时超过 `Cmax`，也可以先在第 3 步被消耗，只有消耗后仍超限的部分才丢弃。

### 欠额批次与上限

- 批次保存为 `(产生分钟, 剩余欠额)`，按产生时间严格递增，剩余欠额始终为正。
- 追加新批次前，用 `room = Dmax - 当前未偿欠额总量` 计算可用额度；同分钟入账偿还腾出的额度立即可用。
- 新批次大小为 `min(need, room)`；超出 `room` 的部分计入本分钟被限速量。
- 批次到期后不再接受本分钟入账偿还，必须先计费再入账。
- 从无限模式切到标准模式时，所有剩余欠额立即按 `price` 计费并清空；之后不会重复到期计费。
- 标准切无限或同模式切换没有费用，也没有其他副作用。

### 拒绝顺序与并发

- 构造参数越界时整体拒绝，不返回半初始化计量器。
- `Tick` 按顺序只返回第一个错误：参数非法（`m<0` 或 `u` 越界）、序号回退（`m<next`）、序号缺口（`m>next`）。
- `SetMode` 仅在模式不是 `0` 或 `1` 时以参数非法拒绝。
- 被拒绝的操作不修改任何状态，包括 `next`。
- 所有方法通过互斥保护；查询返回批次副本，并发调用结果等价于某个串行顺序。

### 本地验证

```bash
# 全量测试；测试包含 2000 组随机序列与独立朴素模拟对照
go test ./...

# 查看随机序列的输入、输出和判定依据
go test -v -run TestRandomSequencesMatchNaiveSimulation ./burstmeter

# 竞态检测
go test -race ./...
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
