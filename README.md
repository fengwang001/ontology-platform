# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- `whiteboard/`：多人实时协作白板的元素叠放与编辑锁服务。
  元素全序（底到顶）、组合整体移动、软锁与乐观版本冲突，全部操作可并发调用，
  结果等价于某个串行顺序。设计取舍与验证方法见 [DESIGN.md](DESIGN.md)。

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
