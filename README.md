# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- [directorycache](directorycache/README.md)：目录式缓存一致性协议模拟器，包含私有缓存、目录持有者集合、失效、降级、写回与 LRU 淘汰。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 运行目录缓存模拟器测试
go test -v ./directorycache
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./directorycache
go test -run TestRandomOperationsMatchSingleMemoryReference ./directorycache

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
