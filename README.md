# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 组件

- `scheduler/`：事件驱动的多路径传输连接发送侧调度器（子流选路、
  子流/连接级确认、失效重新注入、连接级接收窗口）。设计取舍与验证
  方法见 `scheduler/DESIGN.md`。

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
