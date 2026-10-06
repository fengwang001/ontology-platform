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

# 教师授课分配与工作量核算服务（teaching 包）
# 随机差分测试逐步日志（输入/输出/判定依据）
go test -race -run TestRandomDifferential -v ./teaching/
# 冲突判定不随历史任务数增长的经验证据
go test -run='^$' -bench=BenchmarkConflictLookup ./teaching/
```

## 教师授课任务分配服务

- 代码：`teaching/`
- 设计说明（关键取舍、被放弃方案、口径与验证）：`docs/design.md`
- API 与错误码使用文档：`docs/usage.md`

## 代码检查

```bash
gofmt -l .
go vet ./...
```
