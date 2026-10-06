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

## 行政许可并联审批服务（`permit` 包）

事件溯源 + 纯函数惰性派生实现的并联审批办理服务：工作日时限、补正暂停/恢复、
超时默认通过、一环节不通过的整件终止、申请人撤回，以及任意历史时刻的精确可复现查询。

- 设计与取舍、被放弃方案、本地验证：`docs/permit-design.md`
- API 说明：`permit/doc.go`
- 关键测试：`permit/edge_test.go`（边界）、`permit/diff_test.go`（与独立朴素模型随机对照）、
  `permit/bench_test.go`（开销不随在办许可总数增长）

```bash
go test ./permit/ -race -v
go test ./permit/ -run TestRandomDifferential -v   # 逐步输入/输出/判定依据日志
go test ./permit/ -bench BenchmarkProgressScaling  # 复杂度验证
```
