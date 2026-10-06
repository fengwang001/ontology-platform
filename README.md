# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 路径锁服务（pathlock）

`pathlock/` 提供仓库级大文件路径锁：推送前对规范化路径加锁，支持创建、
前缀分页查询、持有者释放、管理员强制释放（带审计），以及推送时对一批
路径的全有或全无持锁核验与「校验并顺带释放」。祖先/后代方向对他人排他、
对本人放行；所有操作可并发且结果可串行化。设计取舍见 `pathlock/DESIGN.md`。

```bash
go test -race -v ./pathlock
go test -bench=BenchmarkAcquire -run=^$ ./pathlock
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
