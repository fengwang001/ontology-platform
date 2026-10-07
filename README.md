# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 子系统

- `compensation/`：动作副作用补偿回滚子系统（并行副作用分支、
  依赖感知的补偿顺序、O(1) 补偿就绪判定、跨动作可串行化、
  与朴素串行模型的随机差分对拍）。见
  [`compensation/README.md`](./compensation/README.md) 与
  [`compensation/DESIGN.md`](./compensation/DESIGN.md)。

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
