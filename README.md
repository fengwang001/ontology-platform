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
GOCACHE=/tmp/go-cache-ontology /usr/local/go/bin/go test ./...

# 带竞态检测与详细输出
GOCACHE=/tmp/go-cache-ontology /usr/local/go/bin/go test -race -v ./...

# 单个包 / 单个用例
GOCACHE=/tmp/go-cache-ontology /usr/local/go/bin/go test ./routeexecution
GOCACHE=/tmp/go-cache-ontology /usr/local/go/bin/go test -run TestRandomOperationsMatchNaiveModel ./routeexecution

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 路线时间窗监控

核心包位于 `routeexecution/`，提供按站推演、硬/软时间窗、连续驾驶休息、实际到达上报、取消传播、防抖 ETA、锁定窗口、并发线性化和操作日志。

设计取舍与复杂度证明见 `docs/design.md`，API 示例见 `docs/api.md`。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
