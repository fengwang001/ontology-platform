# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

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

## 组件

- [`materializer/`](materializer/README.md)：全或无原子批次键值物化器。批内顺序预演（前置期望可观察前序写入/删除），全部通过后单次原子指针切换生效；空批、空键、期望失败为三类互不相同的可判定错误，失败无痕。并发读只见完整批次边界。
  - 演示：`go run ./cmd/materializer-demo`
  - 测试：`go test -race -v ./materializer/`
