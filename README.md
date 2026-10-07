# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 包

- `exports/`：模块包导出映射解析器。给定导出映射表与导入请求
  （子路径 + 活动条件集合），解析出内部目标或返回可区分的错误类别。
  支持通配键、嵌套条件映射、显式禁止、原子换表与并发解析；
  单次解析开销与表中键总数无关。详见 `exports/DESIGN.md`。

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
