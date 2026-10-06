# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 光伏余电上网月度结算引擎

实现在 `ontology` 包：`NewEngine(interval)` 创建引擎，`RegisterConsumer` 登记产消者，
`PutReading` 登记/修正间隔读数，`SetParams`/`SetPrices` 按月生效更改，
`SealMonth` 按顺序封账，`MonthResult` 查询月结果（未封账月为零副作用预览），
`CreditBalance` 查询未到期额度余额。月份用 `ontology.ParseMonth("2025-03")` 得到。
错误为 `*ontology.SettError`，按 `Kind` 区分参数非法 / 月份已封账 / 顺序错误 / 数据缺失。
设计取舍见 `DESIGN.md`。

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
