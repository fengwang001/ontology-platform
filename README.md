# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 住院护理给药时间表

实现位于 `medschedule/`：

- 设计与取舍、被放弃方案、性能论证：`docs/DESIGN.md`
- API 与边界语义、错误码：`docs/API.md`
- 独立朴素模型 + 1500 组随机差分（逐步日志 `medschedule/diff_trace.log`）
- 短/长历史两档性能对照（`TestCostIndependentOfHistory`）

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
