# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 已实现模块

- `lifecycle/`：对象逻辑删除与可见性服务。四态状态机
  （存活 / 待撤销宽限 / 已归档 / 保留期冻结）、可撤销宽限期、
  保留期冻结、两类查询身份的可见性、出边跟随源对象可见性、
  四类固定次序错误、单锁线性化并发、只追加审计与逐次操作日志。
  设计说明见 [`docs/design.md`](docs/design.md)。

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
go test -run TestGraceBoundaryExactAndPast ./lifecycle

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

本仓库的 Go 工具链位于 `/usr/local/go/bin`；若默认构建缓存目录只读，
可设置 `GOCACHE=/tmp/gocache GOPATH=/tmp/gopath`。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
