# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 航班候补席位

候补登记、释放兑现、确认期限、过期回流、容量调整和航班取消实现在根包 `ontology`：

- 入口：`NewSystem`、`Register`、`Withdraw`、`ChangePriority`、`Confirm`
- 释放：`CancelConfirmed`、`AdjustCapacity`
- 查询：`Snapshot`
- 设计取舍与性能论证：`DESIGN.md`
- 随机朴素模型对照与逐步日志：`naive_test.go`

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
