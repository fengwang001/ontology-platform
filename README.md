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

## 子组件

- `bitemporal/`：双时态快照格式版本兼容判定组件（有效时间轴/事务时间轴
  在版本升降级时的保留、损失报告、边界语义不兼容、固定填充规则、
  多跳路径一致性）。说明文档见 `bitemporal/DESIGN.md`。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
