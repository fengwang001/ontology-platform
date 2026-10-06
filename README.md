# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 子模块

- `bus/`：公交线路车辆排班与串车调整服务（发车间隔控制、扣车 / 跳站 /
  备车插入、司机工时约束、可复现重放与朴素模型随机对照）。见 `bus/README.md`
  与 `bus/DESIGN.md`。

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
