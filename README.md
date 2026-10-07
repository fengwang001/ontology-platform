# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 分片缓存 `chunkcache`

`chunkcache/` 为内容分发边缘节点的大对象分片缓存：固定大小切片、部分命中、
仅对缺失连续段回源、版本变化作废与一次重判、并发同切片单次回源、LRU 容量约束。
设计与取舍见 [DESIGN.md](DESIGN.md)。

```bash
go test ./chunkcache/ -v
go test -race ./chunkcache/
CC_VERBOSE=1 go test ./chunkcache/ -run TestRandomDifferential -v
```

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
