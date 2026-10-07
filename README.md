# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 功能

- 本体实例的版本化存储（快照 + 增量日志）；
- **具备崩溃原子性的批量更新**：任意时刻中断后重启恢复，批次只会收敛到
  "整批完全生效"或"整批完全未生效"两种终态之一；恢复幂等可重入；
  未决批次写集内的实例在终态确定前拒绝一切读写。
  设计取舍与验证方法详见 [docs/DESIGN.md](docs/DESIGN.md)。

## 代码结构

- `ontology/` —— 核心库：模拟磁盘（`disk.go`）、实例存储（`store.go`）、
  按批次分段的 WAL（`journal.go`）、分阶段执行管线与故障注入（`engine.go`）、
  幂等恢复（`recover.go`）、朴素全量重放参照模型（`naive.go`）、
  审计日志（`audit.go`）；
- `cmd/server/` —— 演示程序；
- `docs/DESIGN.md` —— 设计说明（关键取舍、被放弃的方案、本地验证方法）。

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
