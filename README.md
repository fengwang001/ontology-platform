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

## 分时电价结算引擎

- 包入口：`pricing.NewEngine(loc)`，`nil` 时区使用 UTC。
- 核心操作：`RegisterTariff`、`SetHoliday`、`RegisterReading`、`CorrectReading`、`DeleteReading`、`Bill`、`CloseMonth`。
- 错误可通过类型断言读取 `pricing.Error.Kind()`，类别为 `参数非法`、`月份已封账`、`时序错误`、`读数倒退`、`读数不足`。
- 账单的 `Lines` 按单价汇总电量与金额；无适用版本的电量列入 `UnpriceableEnergy`。
- 设计取舍与性能论证见 `pricing/DESIGN.md`。
- 随机对照测试固定种子，`go test -run Random -v ./pricing` 会打印每条输入、输出和判定依据。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
