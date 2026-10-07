# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- `ontology/`：基于动态标签的属性级权限模块。敏感标签由实例属性当前
  取值按声明规则实时判定（不落盘、无缓存），授权表按标签声明主体的
  可读/可写/可见范围；支持 MVCC 可重复读快照、deny-wins 冲突合并、
  固定优先级的错误分类与完整判定日志。设计取舍见
  [docs/design.md](docs/design.md)。

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
