# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 组件

- `filterview/`：行级过滤视图增量维护。源表中满足左闭右开区间 `[Low, High)` 的行构成视图；随插入/删除/更新实时输出净变化（先撤回旧值、再写入新值），批处理原子提交，支持并发一致快照。详见 `filterview/README.md`。

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
