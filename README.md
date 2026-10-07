# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 功能

- MVCC 版本化图存储：对象/链接的增删带版本号与墓碑，支持确定快照读。
- 分页游标图遍历服务：多跳扩展、每跳限额、静默丢弃 / 显式截断标记
  两种运行模式、一次性游标、快照隔离（遍历期间的并发修改不影响
  本次遍历结果）。设计取舍详见 [DESIGN.md](DESIGN.md)。

## 代码结构

- `ontology/` — 存储（`store.go`）、遍历迭代器（`traversal.go`）、
  游标（`cursor.go`）、分页服务（`service.go`）及全部测试。
- `cmd/server/` — HTTP JSON 入口（对象/链接变更 + `/v1/traverse/page`）。

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
