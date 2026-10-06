# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 包

- `ontology/exports`：模块包导出映射（exports map）解析器。给定导出映射表与导入
  请求（子路径 + 活动条件集合），解析出内部目标或可区分的错误；支持并发解析与
  整表原子替换，单次解析开销与键总数无关。设计说明见
  [exports/DESIGN.md](exports/DESIGN.md)。

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
