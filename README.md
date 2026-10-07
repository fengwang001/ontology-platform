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

## 核心子系统

`ontology` 包实现“带基数约束的链接更新 + 乐观提交 + 有限重试”：
每次重试在实例锁内重新读取最新基线与全部关联，按
“版本冲突 > 基数不满足 > 重试耗尽”的固定顺序给出三类互斥结果，
失败尝试不产生任何可观察变化，基数判定为 O(1)。

- 设计说明（取舍、被放弃方案、验证方法、测试覆盖索引）：`docs/DESIGN.md`
- 实现：`ontology/`（`store.go`、`submit.go`、`errors.go`、`types.go`）
- 服务演示：`cmd/server`（409 版本冲突 / 422 基数拒绝 / 503 重试耗尽）

若默认 Go 构建缓存目录只读，可指定 `export GOCACHE=/tmp/gocache`。
