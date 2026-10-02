# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 组件

- `epoll/`：多实例共享文件状态的 epoll 风格就绪事件队列，支持水平触发、边沿触发、一次性、独占唤醒、FIFO 就绪链与原子回填；语义、复杂度和验证方法见 `epoll/README.md`。

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
