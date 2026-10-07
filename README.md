# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 子系统：对象实例存储与双时态可见性

- `store/` — 实例持久化与版本仲裁：乐观并发写入、系统时间单调分配、
  逻辑删除与复活、拒绝次序（参数非法 > 凭证冲突 > 业务边界）、WAL 与重放。
- `index/` — 单主键双时态索引：持久化 treap，按（系统时间, 业务时间）
  双坐标 `O(log n)` 定位版本，带访问节点计数器用于复杂度证明。
- `service/` — 对外读写门面：凭证格式 `v<N>`、错误归一化
  （`INVALID_ARGUMENT` / `CONCURRENCY_CONFLICT` / `BIZ_BOUNDARY_VIOLATION`）、
  查询状态（`FOUND` / `DELETED` / `NO_VERSION_AT_TIME` / `NEVER_WRITTEN`）。
- `cmd/server/` — 端到端演示：写入、删除、复活、双时态回溯、WAL 落盘。

设计取舍、被放弃方案与复杂度证明方法详见 [docs/design.md](docs/design.md)。

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
