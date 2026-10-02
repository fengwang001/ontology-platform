# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 包

- `overagg`：事件时间 OVER 范围聚合器，按水位线推进释放缓冲行并输出
  范围帧 `[ts-R, ts]` 内的求和、计数与最大值，支持迟到丢弃、补发与
  状态清理。语义与增量维护详见 [overagg/README.md](overagg/README.md)。

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
