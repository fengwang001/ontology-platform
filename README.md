# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 包

- `cpe`：执业资格继续教育学分周期核算服务（学分登记/更正/撤销、周期达标判定、
  宽限补修、超额结转、证书失效与换发、历史时刻查询）。设计说明见
  `docs/cpe-design.md`，测试见 `cpe/*_test.go`。

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
