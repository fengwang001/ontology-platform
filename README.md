# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 子系统：多仓库存可承诺量与订单承诺（atp/）

电商多仓库存的可承诺量（ATP）查询与订单承诺系统，支持现货、
计划入库、带到期时刻的预留与仓库优先序共同作用下的发货仓选择、
限定范围内拆分、永久/暂时缺货区分。设计说明见
[docs/design.md](docs/design.md)。

```bash
# 运行演示
go run ./cmd/atpdemo

# 测试（含朴素模型随机对拍、竞态检测）
go test -race ./atp/...
go test -v -run TestRandomizedDifferential ./atp/service
```

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
