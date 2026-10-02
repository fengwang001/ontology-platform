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

## 突发积分计量器（`burstmeter` 包）

`burstmeter` 实现带封顶溢出、先偿欠额与到期计费的突发积分实例计量器，全部操作互斥、可并发调用。积分以厘积分为单位：每分钟入账 `e = n×b`，利用率为 `u` 百分点的一分钟消耗 `x = n×u`。

### 状态

- `bal`：余额（初值 0，恒满足 `0 ≤ bal ≤ Cmax`）。
- `lb`：启动积分（初值 `L`，只被消耗；不入账、不封顶、不用于偿还欠额；单调不增）。
- 欠额批次队列：每批 `(产生分钟, 剩余欠额)`，按产生分钟严格递增、剩余欠额恒正，未偿总量不超过 `Dmax`。
- `next`：下一个应处理的分钟序号（初值 0）；当前模式（0 标准 / 1 无限）。
- 累计量：入账、消耗、偿还、丢弃、被限速、费用。

### Tick(m, u) 每分钟四步次序

次序固定，不得调换：

1. **到期计费**：凡批次满足 `m − 产生分钟 ≥ W`（恰等即到期，少 1 不计费），其剩余欠额乘 `price` 计入本分钟费用并移除。
2. **入账**：`e = n×b` 先按 FIFO 偿还欠额批次（最旧的先偿，直到 `e` 用完或批次为空），剩余部分才加进 `bal`。
3. **消耗**：`x = n×u` 先用 `lb`（`u1 = min(lb, x)`），再用 `bal`（`u2 = min(bal, x−u1)`）；不足额 `need = x−u1−u2`：
   - 无限模式：`room = Dmax − 偿还后的未偿欠额总量`，追加批次 `(m, min(need, room))`（为 0 不追加）；`need − min(need, room)` 计入被限速量。同分钟入账偿还腾出的额度可立即被新批次使用。
   - 标准模式：不产生批次，全部 `need` 计入被限速量。
4. **封顶**：封顶发生在消耗之后。`bal > Cmax` 时超出部分计入累计丢弃量、`bal` 取 `Cmax`（因此同分钟入账即便暂时超过上限，只要随后被消耗用掉就不丢弃）。

返回本分钟费用、`bal`、未偿欠额总量、本分钟丢弃量与被限速量；随后 `next = m+1`。

先到期后入账是关键顺序：到期批次在入账之前移除，避免本分钟入账把本应到期计费的欠额“免费偿清”。

### SetMode(mode)

- 无限 → 标准：全部未偿批次的剩余欠额立即按 `price` 计费并清空队列，返回该费用；此后不会再对这些批次触发到期计费。
- 其余切换（标准 → 无限、同模式）无费用、无其他副作用。

### 拒绝规则（被拒绝不改变任何状态，包括 `next`）

- 构造：任一参数越界统一返回 `ErrInvalidConfig`，整体拒绝。
- `Tick` 按顺序只报第一个：参数非法（`m < 0` 或 `u` 不在 `[0,100]`，`ErrInvalidArgument`）→ 序号回退（`m < next`，`ErrSequenceFallback`）→ 序号缺口（`m > next`，`ErrSequenceGap`）。
- `SetMode` 仅当 `mode` 不是 0/1 时以 `ErrInvalidArgument` 拒绝。

### 本地验证

```bash
# 全量测试（2000 组随机序列与独立朴素模拟器逐步差分对照）
go test -v ./burstmeter/

# 逐条打印输入、实测/朴素输出与判定依据
go test -v -verbose -run TestRandomAgainstNaive ./burstmeter/

# 竞态检测
go test -race -count=1 ./burstmeter/
```

差分测试逐操作比对返回值、拒绝原因、余额、启动积分、批次队列、模式、序号与六项累计量，并校验余额边界、`lb` 单调、欠额上限、批次严格递增以及入账守恒式：`累计入账 = Δbal + bal 支付的消耗 + 偿还量 + 丢弃量`。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
