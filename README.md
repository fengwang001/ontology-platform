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

## 带宽采样计费结算器（`billing` 包）

`billing` 包实现按带宽采样计费的单周期结算器，支持乱序/重复/更正采样、
版本化撤回、随时查询计费速率与一次性封存结算。

```go
s := billing.NewSettler(startUnix, n) // n 个 300 秒槽位
err := s.Submit(billing.Sample{Slot: 3, Ingress: 900, Egress: 120, Version: 2})
rate, err := s.CurrentRate()           // 随时查询，期望 O(log K)
res, err := s.Settle(committedRate, toleranceBp) // 结算并封存
o := s.Overview()                      // 已收/缺失槽位、费率、封存状态
```

关键设计与口径（丢弃 `floor(5K/100)` 个最大有效值、并列独立占位、撤回
保留版本门槛、错误固定优先级、并发线性化）见 `billing/DESIGN.md`。

测试包含：K=19/20/39/40 丢弃跳变、并列占位、高版本覆盖降费、撤回门槛、
缺失容忍边界、入/出向取大、重复结算、拒绝次序等定向用例；1200 组随机
操作序列与独立朴素模型的差分对照（逐步轨迹写入 `billing/diff_trace.log`）；
以及“访问步数不随 K 线性增长”的实测证明和 `-race` 并发测试。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
