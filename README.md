# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 组件

- [`certcache/`](certcache/README.md)：证书吊销状态响应缓存。乱序响应合并为每证书一条记录，
  吊销为不可逆终态；按有效区间 `[a,b)` 判定可信性；容量满时只淘汰已过期正常记录；
  支持并发提交/判定与全局吊销闩锁。规则说明见 `certcache/README.md`。

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
