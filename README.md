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

# 撤回日志回收器演示（打印输入 / 结果 / 判定依据日志）
go run ./cmd/recall-demo
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

# 撤回日志回收器（按水位回收，含并发竞态检测）
go test -race -v ./recall
go test -race -count=20 ./recall

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

撤回日志回收器的水位与回收规则、回收上界计算方式见 `recall/DESIGN.md`。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
