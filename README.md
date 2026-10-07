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

## 容量受限转发表管理器(fib 包)

`fib/` 实现容量受限的转发表管理器:控制面维护全量路由,数据面
维护逐地址语义完全一致、条目数最少的聚合转发表,更新超出容量
上限时整体拒绝(批量全有或全无)。

```bash
go test ./fib/          # 单元测试 + 1200 组随机序列对照朴素模型
go test ./fib/ -race    # 并发一致性(竞态检测)
```

设计说明(关键取舍、被放弃的方案、验证方法)见
[docs/fib-design.md](docs/fib-design.md)。
