# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 对象实例双时态存储子系统

当前仓库包含“对象实例存储与双时态可见性”子系统，三个协作模块：

- `internal/instance`：实例持久化、系统时间单调分配、乐观并发仲裁、逻辑删除/复活。
- `internal/bitemporal`：每主键独立的持久 treap 双时态索引（同起点覆盖链 + floor 查询 + 历史重建）。
- `internal/api`：对外读写门面、凭证解析、错误归一化（`INVALID_ARGUMENT` / `OPTIMISTIC_CONFLICT` / `BEFORE_EARLIEST_BIZ`）。

设计取舍、被放弃方案与复杂度验证见 `docs/DESIGN.md`，接口用法见 `docs/API.md`。

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
