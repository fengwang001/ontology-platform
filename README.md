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

## 行政许可并联审批

核心实现位于 `approval`，支持工作日时限、补正暂停/恢复、超时默认、DAG 后继启动、整件终止、撤回、历史时刻查询、权限与错误优先级。

```bash
GOCACHE=/tmp/ontology-gocache /usr/local/go/bin/go test -race -v ./approval
GOCACHE=/tmp/ontology-gocache /usr/local/go/bin/go test -run '^$' -bench BenchmarkQueryOneLicense ./approval
```

设计取舍、复杂度证明与朴素模型对照方法见 `docs/parallel-approval.md`。

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
