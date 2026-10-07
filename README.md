# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 功能

对象整体逻辑删除与属性历史记录独立逻辑删除两套机制：

- 整体删除为 O(1) 对象级墓碑，全有或全无，可复活；复活保留各记录的单独删除状态。
- 单条历史记录可独立删除/撤销，不影响同属性其他记录与对象存在性。
- 可见性查询按「对象删除 → 记录单独删除 → 时间最新」三层条件归类互斥原因。
- 详细设计见 [docs/DESIGN.md](docs/DESIGN.md)。

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
