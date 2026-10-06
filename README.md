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

## 电池包保护与电流降额管理器（`battery` 包）

`battery/` 提供按周期采样判定保护状态、输出充/放电方向允许电流并管理人工复位
锁存故障的管理器。模块划分、关键取舍与被放弃方案见 [DESIGN.md](DESIGN.md)。

```go
m, err := battery.New(cfg)                         // 非法配置创建即拒绝
m, _ = battery.New(cfg, battery.WithLogger(log.Printf))
snap, err := m.Submit(battery.Sample{             // 非法采样整体拒绝、不推进时间
    TimeMS: 10, CellVoltagesMV: volts, Temperatures: temps, CurrentMA: 1200,
})
snap.AllowedChargeMA     // 电压表/温度表/额定值取最严
snap.BanCharge           // 过压禁充（确认时长 + 回差释放）
snap.Latched             // 过流 / 压差锁存，两方向恒为 0
_, err = m.Reset(operatorPermitted)                // 按固定次序给出可区分错误
```

所有方法可并发调用，查询返回某个已完成采样之后的一致快照；单次采样开销为
O(单体数+温度点数)，与历史采样总数无关（`BenchmarkSubmit` 可验证）。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
