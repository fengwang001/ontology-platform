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

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## 合乘服务

- 入口：`NewService`、`AddVehicle`、`SubmitOrder`、`UpdatePosition`、`CancelOrder`、`GetOrder`。
- 下单无车可并入时返回 `ErrNoVehicleAvailable`，同时订单进入等待队列。
- `SliceLogger.Print()` 可输出每条操作的输入、输出、错误和判定原因。
- `NaiveModel` 独立保存事件并从零重放，供 `TestRandomReplayAgainstNaive` 对照。
- 查询订单只读取内存 map；车辆不保留完成订单，复杂度不随历史订单数增长。
