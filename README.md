# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 本体快照导出

快照导出协同组件位于 `snapshot/`：

- 设计说明：`docs/design.md`
- 使用示例：`docs/usage.md`
- 核心边界规则：提交号连续、只在 commit 时分配，边界左闭；`CommitLSN == boundary` 固定归入快照。
- 并发模型：所有写入和快照读取经同一 FIFO 队列串行处理。
- 测试：包含边界重合、跨边界事务、引用完整性交织、四类错误优先级、并发竞态和 500 组随机序列朴素模型对照。

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
