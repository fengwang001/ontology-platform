# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 组件

- `edgecache`：内容分发边缘节点的大对象分片缓存。对象按固定大小切片
  缓存，字节范围请求部分命中、只对缺失的连续切片段回源，支持对象版本
  变化（旧版本切片作废 + 请求重判一次）、并发去重回源（singleflight）
  与 LRU 容量约束（pin 保护）。源站通过调用方注入的 `Source` 接口访问。
  设计取舍与验证方法见 [docs/edgecache-design.md](docs/edgecache-design.md)。

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
