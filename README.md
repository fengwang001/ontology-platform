# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）


## 包

- `allocator/`：按检查点纪元回收的数据源拆分分配器（偏好分配、失败回收、
  隔离、精确的「无更多拆分」信号）。设计说明见
  [allocator/README.md](allocator/README.md)，测试见
  `go test ./allocator/`（含 2000 组随机操作序列与朴素模拟的对照）。

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
