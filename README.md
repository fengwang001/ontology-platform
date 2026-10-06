# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- `backup/`：带依赖链保护的分层备份保留管理服务。覆盖全量/增量备份登记、
  日/周/月三层保留代表选取、损坏标记对可恢复性的传递影响、法律保留、
  过期备份批量清理；任意时刻可精确复现“哪些备份必须保留、哪些可以删除、
  为什么”。设计取舍与验证方法见 `backup/DESIGN.md`。

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
