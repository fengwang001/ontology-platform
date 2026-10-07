# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 代码结构

- `ontology/`：核心领域包
  - `types.go`：导入模式、记录语义、失败类别、判定结果与日志等类型定义
  - `store.go`：对象类型/对象/主体/权限条目的存储与发起时刻权限快照
  - `batch.go`：批量导入的属性级权限守门器（原子/宽松两种模式）
- `DESIGN.md`：守门器设计说明（关键取舍、被放弃的方案、本地验证方法）

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
