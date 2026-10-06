# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 分时电价结算引擎（`tou` 包）

`tou/` 实现了多供电点、带生效时刻电价版本、工作日/休息日/节假日日类型的按月结算。
设计与取舍见 `tou/DESIGN.txt`（≤40 行）。

```go
e := tou.New(nil) // 默认固定 UTC+8，保证任何环境日界/月界一致
v := tou.TariffVersion{EffectiveAt: t0}
for i := range v.Schedules { // 三种日类型都必须无缝覆盖 [0,86400)
    v.Schedules[i].Periods = []tou.Period{{StartSec: 0, Price: 100}}
}
e.RegisterTariff(v)
e.SetHoliday("2024-05-01", true)
e.RegisterReading("point-1", t1, 0)
e.RegisterReading("point-1", t2, 100)
b, _ := e.Bill("point-1", monthStart) // 未封账月实时反映最新状态
e.CorrectReading("point-1", t1, 5)   // 仅未封账范围可修正
e.CloseMonth("point-1", monthStart)  // 全片可计价且有月末读数才成功
```

- 单价单位：千分之一货币单位/Wh；金额 `floor(电量*单价/1000)` 向下取整。
- 分摊：片电量 `floor(区间电量*片时长/区间时长)`，余量归区间最后一片，严格守恒。
- 错误（固定次序）：`ErrInvalidParameter > ErrMonthClosed > ErrOutOfOrder >
  ErrReadingRegression > ErrInsufficientReadings`；封账另有 `ErrUnbillable`。
- 差分测试：`TestDifferentialRandom` 用随机操作序列对照独立朴素模型，逐条日志
  写入 `/tmp/tou-diff-*.log`；失败种子可用 `TOU_SEED=<seed>` 精确重放。

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
