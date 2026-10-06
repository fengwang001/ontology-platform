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

## 岗位编制与录用通知服务

实现位于 `staffing/`，独立朴素模型与随机/并发对照测试位于 `staffingtest/`：

- 设计与取舍：`docs/DESIGN.md`
- API 文档：`docs/API.md`
- 逐步判定日志样例：`docs/diff-sample.log`

```bash
export PATH=$PATH:/usr/local/go/bin GOCACHE=/tmp/gocache

go test -race -count=1 ./...
STAFFING_LOG=$PWD/docs/diff-sample.log \
  go test -run TestNaiveDifferential -count=1 ./staffingtest
go test -run=NONE -bench=BenchmarkIssueWithHistory -benchtime=2000x ./staffing
```
